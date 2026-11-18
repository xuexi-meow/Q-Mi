// Package tool 定义工具契约，不依赖具体工具或 Agent 运行时。
package tool

import "encoding/json"

// Tool 一个可被 LLM 调用的工具。Run 收到原始 JSON 参数，返回紧凑 JSON
// 字符串；Run 为 nil 表示纯标记工具（finish），由循环特殊处理、不执行。
// Usage/Asyncable 与定义同处一地：加工具 = 写齐本结构体，文档与 schema 自动生成。
type Tool struct {
	Name        string
	Description string // 一句话（schema 用，模型选工具的第一线索）
	Parameters  any
	// Usage 完整使用说明（markdown）：参数明细/最小示例/错误形态。
	// 随 PromptDocs 注入 system 静态区；必须确定性（禁时间戳/随机）。
	Usage string
	// Asyncable 允许模型以 {"_async":true,...} 调用走后台任务（方案 B）。
	// 仅慢 IO 类工具声明；Asyncable 工具的 Usage 里必须写明异步语义。
	Asyncable bool
	Run       func(args json.RawMessage) (string, error)
}
