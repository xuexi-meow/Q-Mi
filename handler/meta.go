package handler

import "Q-Mi/event"

// 请求事件(好友/加群申请)与元事件(心跳/生命周期)处理

func init() {
	event.RegisterTyped("request.friend", func(e *event.OB11FriendRequestEvent) {
		// TODO: 好友申请处理
	})

	event.RegisterTyped("request.group", func(e *event.OB11GroupRequestEvent) {
		// TODO: 加群申请处理
	})

	event.RegisterTyped("meta.heartbeat", func(e *event.OB11HeartbeatEvent) {
		// TODO: 心跳监控
	})

	event.RegisterTyped("meta.lifecycle", func(e *event.OB11LifeCycleEvent) {
		// TODO: 生命周期处理
	})
}
