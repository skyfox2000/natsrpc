package main

import (
	"context"
	"fmt"
	natsrpc "skyfox2000/nats-rpc"
	"time"
)

func main() {
	opt := natsrpc.Option{
		Host:     "nats://172.19.158.204:4222",
		ClientId: "test1",
		GroupId:  "group1",
	}

	fmt.Println("start")
	broker1 := natsrpc.NewBroker(opt)
	// broker1.Register("add", func(data interface{}) (interface{}, error) {
	// 	return nil, nil
	// })

	startTime := time.Now()
	opt.ClientId = "test2"
	broker2 := natsrpc.NewBroker(opt)
	broker2.Register("minus", func(data interface{}) (interface{}, error) {
		return fmt.Sprintf("Hello, %v", data), nil
	})

	for i := 0; i < 10000; i++ {
		func(index int) {
			natsrpc.Ants.Submit("NATSRPC-Test", func() {
				test(broker1, startTime, index)
			}, 10000)
		}(i)
	}

	for i := 0; i < 10000; i++ {
		func(index int) {
			natsrpc.Ants.Submit("NATSRPC-Test", func() {
				test(broker1, startTime, index)
			}, 10000)
		}(i)
	}

	time.Sleep(time.Second * 3)
}

func test(broker1 *natsrpc.Broker, n time.Time, index int) {
	broker1.Call(context.TODO(), "minus", "Alex "+fmt.Sprintf("%v", n.Format("2006-01-02 15:04:05.999999")))
	fmt.Println("cost time n :", index, time.Since(n))
}
