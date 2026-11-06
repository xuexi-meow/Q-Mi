// Package handler 承载所有事件的处理逻辑(业务层)。
//
// 数据层:Q-Mi/event 只定义事件结构体(纯数据)。
// 逻辑层:本包在 init() 里用 event.Register / event.RegisterTyped
// 把处理函数挂到对应事件上,分发仍然由 event.Dispatch 驱动。
//
// 文件分工:
//
//	handler.go  本文件(包说明)
//	message.go  消息事件:message.private / message.group
//	notice.go   通知事件:notice.* 与 notice.notify.*(含戳一戳)
//	meta.go     请求事件 request.* 与元事件 meta.*
//
// 新增事件处理:在对应文件里加一行 RegisterTyped 即可。
// 新增事件类型:先在 event/event.go 定义结构体,再到这里注册。
package handler
