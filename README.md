# natsrpc
rpc call base on NATS STREAMING, using Pub/Sub and channel transfer data

## 用法
```go
  // 初始化配置
	opt := natsrpc.Option{
		Host:     "nats://localhost:4222",
		ClientId: "test1",
		GroupId:  "group1",
	}

	fmt.Println("start")
  // 第一个节点配置
	broker1 := natsrpc.NewBroker(opt)

	startTime := time.Now()
	opt.ClientId = "test2"
  // 第二个节点配置
	broker2 := natsrpc.NewBroker(opt)
  // 第二个节点提供的服务接口
	broker2.Register("minus", func(data interface{}) (interface{}, error) {
		return fmt.Sprintf("Hello, %v", data), nil
	})

  // 第一个节点调用第二个节点的数据
	for i := 0; i < 10000; i++ {
		func(index int) {
			natsrpc.Ants.Submit("NATSRPC-Test", func() {
				test(broker1, startTime, index)
			}, 10000)
		}(i)
	}
```

## Git commit ⻛格指南

- feat: 增加新功能
- fix: 修复问题
- style: 代码⻛格相关⽆影响运⾏结果的
- perf: 优化/性能提升
- refactor: 重构
- revert: 撤销修改
- test: 测试相关
- docs: ⽂档/注释
- chore: 依赖更新/脚⼿架配置修改等
- ci: 持续集成

## 许可证

该项目基于 MIT 许可证进行分发。更多详情请参阅 [LICENSE](LICENSE) 文件。
