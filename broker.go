package natsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	stan "github.com/nats-io/go-nats-streaming"
)

type Broker struct {
	rwmutex        *sync.RWMutex
	option         Option
	config         []stan.Option
	client         stan.Conn
	subscriptions  map[string]topicService
	incommingCalls sync.Map
}

type topicService struct {
	service      func(data interface{}) (interface{}, error)
	subscription stan.Subscription
}

// Meta数据
type metaData struct {
	InboxTopic string
	ReqId      string
	Time       int64
}

// 请求数据
type requestData struct {
	Data     interface{}
	Metadata metaData
}

// 结果数据
type responseData struct {
	Data     interface{}
	Err      string
	Metadata metaData
}

// 参数配置
type Option struct {
	// 主机地址
	Host string
	// 集群ID
	ClusterId string
	// 客户端ID
	ClientId string
	// 分组ID
	GroupId string
	// 节点ID
	NodeId string
	// 注册Topic名中是否包含节点ID
	UseNodeTopic bool
	// 结果接收Topic数量
	InboxSize int
	/// 并发数量
	Concurrent int
}

func NewBroker(option Option) *Broker {
	broker := &Broker{
		rwmutex: &sync.RWMutex{},
	}

	broker.checkOption(&option)
	broker.option = option
	broker.subscriptions = make(map[string]topicService)
	broker.incommingCalls = sync.Map{}
	broker.connect(&option)

	broker.registerInbox(option)

	return broker
}

// 检查和设置默认参数
func (b *Broker) checkOption(option *Option) {
	if option.Host == "" {
		option.Host = "nats://localhost:4222"
	}

	if option.ClusterId == "" {
		option.ClusterId = "test-cluster"
	}

	if option.ClientId == "" {
		option.ClientId = "test-client"
	}

	if option.InboxSize == 0 {
		option.InboxSize = 5
	}

	if option.Concurrent == 0 {
		option.Concurrent = 100000
	}
}

// 连接服务器
func (b *Broker) connect(option *Option) {
	if b.config == nil {
		b.config = []stan.Option{
			stan.NatsURL(option.Host),
			stan.SetConnectionLostHandler(func(c stan.Conn, err error) {
				// 处理连接丢失的情况
				c.Close()
				b.client = nil
				go b.connect(option)
			}),
		}
	}

	fmt.Println("Connecting to NATS...", option.Host, option.ClusterId, option.ClientId)
	client, err := stan.Connect(option.ClusterId, option.ClientId, b.config...)
	if err != nil {
		fmt.Println("Err:  ", client, err.Error())
		return
	}

	b.rwmutex.Lock()
	b.client = client
	b.rwmutex.Unlock()
	// 重新连接到NATS
	for key := range b.subscriptions {
		b.Register(key, b.subscriptions[key].service)
	}
}

// RPC调用
func (b *Broker) Call(
	ctx context.Context,
	serviceName string,
	params interface{}) (interface{}, error) {
	if b.client == nil {
		return nil, errors.New("NATS client is not connected")
	}

	resultChan := make(chan responseData, 1)

	reqId := strings.ReplaceAll(uuid.NewString(), "-", "")
	serviceName = strings.ToLower(serviceName)
	topic := fmt.Sprintf("rpc:%s", serviceName)
	inboxTopic := b.getInboxTopic(b.option)

	go Ants.Submit("NATSRPC-Pub", func() {
		// 生成一个随机的请求ID
		params = requestData{
			Data: params,
			Metadata: metaData{
				InboxTopic: inboxTopic,
				ReqId:      reqId,
				Time:       time.Now().UnixMilli(),
			},
		}
		b.incommingCalls.Store(inboxTopic+":"+reqId, resultChan)

		// TODO: 考虑不同的序列化方式
		data, _ := json.Marshal(params)
		b.client.Publish(topic, data)
	}, b.option.Concurrent)

	var result responseData
	select {
	case result = <-resultChan:
		// 处理返回结果
	case <-ctx.Done():
		// 处理超时或取消的情况
		close(resultChan)
		b.incommingCalls.Delete(inboxTopic + ":" + reqId)
		return nil, errors.New("rpc-call timeout or cancelled")
	}

	return result.Data, errors.New(result.Err)
}

// 注册服务
func (b *Broker) Register(
	serviceName string,
	serviceFunc func(data interface{}) (interface{}, error)) error {

	if b.client == nil {
		return errors.New("NATS client is not connected")
	}
	var topic string
	if strings.HasPrefix(serviceName, "rpc:") {
		topic = serviceName
	} else {
		nodeId := strings.ToLower(b.option.NodeId)
		if b.option.UseNodeTopic && nodeId != "" {
			nodeId = nodeId + ":"
		} else {
			nodeId = ""
		}
		serviceName = strings.ToLower(serviceName)
		topic = fmt.Sprintf("rpc:%s%s", nodeId, serviceName)
	}

	sub, _ := b.client.Subscribe(topic,
		func(msg *stan.Msg) {
			go Ants.Submit("NATSRPC-Sub", func() {
				reqData := &requestData{}
				result, err := b.handleRequest(msg, reqData, serviceFunc)

				var errStr string
				if err != nil {
					errStr = err.Error()
				}
				resData := &responseData{
					Data: result,
					Err:  errStr,
					Metadata: metaData{
						ReqId: reqData.Metadata.ReqId,
						Time:  time.Now().UnixMilli(),
					},
				}
				b.response(reqData, resData)
			}, b.option.Concurrent)
		})

	b.rwmutex.Lock()
	b.subscriptions[topic] = topicService{
		service:      serviceFunc,
		subscription: sub,
	}
	b.rwmutex.Unlock()

	return nil
}

// 处理请求
func (b *Broker) handleRequest(msg *stan.Msg, reqData *requestData, serviceFunc func(data interface{}) (interface{}, error)) (interface{}, error) {
	resultChan := make(chan interface{}, 1)
	errChan := make(chan error, 1)
	done := make(chan bool, 1)
	// 创建定时context
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	json.Unmarshal(msg.Data, reqData)

	go Ants.Submit("NATSRPC-Serv", func() {
		// 处理rpc请求
		r, e := serviceFunc(reqData.Data)
		resultChan <- r
		errChan <- e
		close(done)
	}, b.option.Concurrent)
	var finalResult interface{}
	var finalErr error
	select {
	case <-done:
		// defer close(resultChan)
		// defer close(errChan)
		finalResult = <-resultChan
		finalErr = <-errChan
		// 处理返回结果
	case <-timeoutCtx.Done():
		// 处理超时或取消的情况
		return nil, errors.New("rpc-call timeout or cancelled")
	}

	return finalResult, finalErr
}

// 注册结果接收Topic
func (b *Broker) registerInbox(option Option) error {
	if b.client == nil {
		return errors.New("NATS client is not connected")
	}
	inboxTopic := b.getInboxTopic(option)
	b.client.Subscribe(inboxTopic,
		func(msg *stan.Msg) {
			// 处理rpc响应
			resData := &responseData{}
			b.handleResponse(msg, resData)
		})
	return nil
}

// 处理结果
func (b *Broker) handleResponse(msg *stan.Msg, resData *responseData) {
	json.Unmarshal(msg.Data, resData)
	reqId := resData.Metadata.ReqId
	resData.Metadata.ReqId = ""
	inbox, _ := b.incommingCalls.LoadAndDelete(msg.Subject + ":" + reqId)
	if inbox != nil {
		inbox.(chan responseData) <- *resData
		close(inbox.(chan responseData))
	}
}

// 获得一个返回结果的Topic
func (b *Broker) getInboxTopic(option Option) string {
	nodeId := strings.ToLower(b.option.NodeId)
	if b.option.UseNodeTopic && nodeId != "" {
		nodeId = nodeId + ":"
	} else {
		nodeId = ""
	}
	return fmt.Sprintf("rpc:%s:%sresponse", option.ClientId, nodeId)
}

// 发送结果
func (b *Broker) response(reqData *requestData, resData *responseData) {
	// 考虑不同的序列化方式
	data, _ := json.Marshal(*resData)
	// 发送结果
	b.client.Publish(reqData.Metadata.InboxTopic, data)
}
