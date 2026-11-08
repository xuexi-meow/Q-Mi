// agent/context.go 实现上下文组装（spec P2 §2.2/§3.5）：缓存分层
// system(纯静态) → 全会话混合历史(只追加,时间水印块裁剪) → [思考笔记]+情绪+本轮新消息(末尾,含当前时间)。
// P2.7 多模态：multimodal 开启时，带图片的历史行/新消息行 content 变为
// OpenAI 多模态数组（文本部分 + image_url 部分）。
// ContextBuilder 是可替换接缝，DefaultBuilder 为默认实现。
package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// fallbackPersona persona 文件缺失时的内置兜底提示词。
const fallbackPersona = "你是一个QQ群聊/私聊里的AI成员，用中文自然交流。"

// agentGuideText guide 文件缺失时的内置兜底格式说明（正式版在 agent/guide.md）。
const agentGuideText = `[Agent 工作方式]
你在思考循环中：可先调用工具查资料、记笔记、调整状态，再决定是否回复。
- 要说话必须调用 send_message（可多次调用，分条发送）；不调用即本轮不回复。
- think(note) 记录思考笔记，下一轮对话可见；update_mood(mood) 更新你的情绪。
- 查历史用 get_recent_messages，搜历史用 search_messages；工具用法见 system 提示里的工具使用说明。
- 想结束本轮思考就调用 finish；finish 前已发出的消息均已生效。`

// defaultGuidePath guide_file 未配置时的默认路径。
const defaultGuidePath = "agent/guide.md"

// Turn 进 builder 的一轮输入：chat 上下文、思考笔记、本轮新消息、当前时间。
// 携带全部原料，任何自定义 builder 都能拿到。
type Turn struct {
	ChatType    string // 本轮新消息所属会话类型，"private" / "group"（SendLLM 回复用）
	ChatID      int64  // 本轮新消息所属会话：群号；私聊 = 对方 QQ
	SelfID      int64  // 机器人自身 QQ
	Now         time.Time
	Mood        string        // 当前情绪状态（manager 从 state.json 读出），注入末尾 user 块头部（进 system 会击穿缓存前缀）
	Notes       []Note        // 思考笔记（manager 从 notes.json 读出），注入末尾 user 块头部
	NewMessages []TurnMessage // 每条自带所属会话（历史已跨会话混合）
	// pendingImages 本轮工具（view_message_images/查询自动带图）申请查看的
	// 图片，data URL 形态。工具 Run 追加，handleTurn 在每轮工具执行完后把
	// 它们作为一条 user 多模态消息附加进思维链并清空。同轮多工具并发执行，
	// 追加须经 AddPendingImage（pendingMu 保护）；清空只在 worker 串行段。
	pendingImages []string
	pendingMu     sync.Mutex
}

// AddPendingImage 申请把一张图片附到本轮下一条消息（并发工具安全）。
func (t *Turn) AddPendingImage(du string) {
	if t == nil {
		return
	}
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	t.pendingImages = append(t.pendingImages, du)
}

// TurnMessage 本轮防抖窗口内的一条新消息。
type TurnMessage struct {
	MessageID  int64  // OneBot 消息 ID（图片以 (message_id, 图片序号) 为锚做内容寻址缓存）
	ChatType   string // 该消息所属会话类型 "private" / "group"
	ChatID     int64  // 该消息所属会话 ID：群号；私聊 = 对方 QQ
	SenderID   int64
	SenderName string
	Text       string
	Images     []imageRef // 消息里的图片段（P2.7 多模态，wiring 提取；URL 可能失效）
	IsSelf     bool
	Time       time.Time
}

// imageRef 一张图片段：Idx 为该消息内 image 段的序号（含缺 url 的段，保证
// 与 storage.message_images 映射锚一致），URL 为下载来源（QQ 临时链，可能
// 已失效，失效时经映射/磁盘兜底）。
type imageRef struct {
	Idx int
	URL string
}

// ContextBuilder 上下文组装接口。agent 流程只依赖该接口，
// 替换实现 = 换一个构造参数，不动队列/触发/发送逻辑。
type ContextBuilder interface {
	Build(turn *Turn) []ChatMessage
}

// DefaultBuilder 默认实现：persona + agent 格式说明 + 块裁剪历史 + 末尾新消息。
type DefaultBuilder struct {
	store      *storage.Store
	max        int
	trim       int
	persona    string
	guide      string
	multimodal bool        // P2.7：开启时带图片的消息行以多模态内容直送模型
	images     *imageStore // 内容寻址图片存储（多会话 worker 共享，磁盘为事实源）
	toolDocs   string      // 工具文档（PromptDocs 输出，system 静态区）

	// mu/anchorTime 历史窗口裁剪锚：anchorTime 为当前窗口最早一条消息的
	// 时间（秒，含亚秒）。窗口 = 时间 ≥ 锚的行，只在窗口超过 max 时整块
	// 推进 trim——两次推进之间窗口头部字节级不变，prompt cache 前缀稳定。
	// 若按"最近 N 条"相对条数裁剪，查询被 Limit 截断后窗口会随每条新消息
	// 滑动一格，导致每次 turn 历史段全量未命中。多会话 worker 共享 builder，
	// 锚的读写以 mu 保护（并发下锚只单调推进，最多损失一次命中）。
	mu         sync.Mutex
	anchorTime float64
}

// Images 暴露图片下载缓存（manager 的 view_message_images 工具与历史多模态
// 共用同一份正/负缓存）。
func (b *DefaultBuilder) Images() *imageStore { return b.images }

// 编译期保证 DefaultBuilder 满足接口。
var _ ContextBuilder = (*DefaultBuilder)(nil)

// NewDefaultBuilder 构造默认 builder。persona/guide 文件缺失则告警一次
// 并使用内置兜底文本，不阻断运行。
func NewDefaultBuilder(store *storage.Store, cfg config.AgentConfig) *DefaultBuilder {
	imgDir := cfg.ImageDir
	if imgDir == "" {
		imgDir = defaultImagesDir
	}
	b := &DefaultBuilder{
		store:      store,
		max:        cfg.History.Max,
		trim:       cfg.History.Trim,
		multimodal: cfg.MultimodalEnabled(),
		images:     newImageStore(imgDir, store),
	}
	data, err := os.ReadFile(cfg.PersonaFile)
	if err != nil {
		slog.Warn("读取 persona 文件失败，使用内置兜底提示词", "path", cfg.PersonaFile, "err", err)
		b.persona = fallbackPersona
	} else {
		b.persona = strings.TrimSpace(string(data))
	}
	guidePath := cfg.GuideFile
	if guidePath == "" {
		guidePath = defaultGuidePath
	}
	if data, err := os.ReadFile(guidePath); err != nil {
		slog.Warn("读取 agent 格式说明文件失败，使用内置兜底文本", "path", guidePath, "err", err)
		b.guide = agentGuideText
	} else {
		b.guide = strings.TrimSpace(string(data))
	}
	return b
}

// 历史消息行时间格式：[MM-DD HH:MM]
const timeLayout = "01-02 15:04"

// 新消息时间头格式：完整日期 + 时间
const nowLayout = "2006-01-02 15:04"

const (
	// maxContentRunes 单条消息内容字符数上限，超出截断（spec 第 10 节）。
	maxContentRunes = 20000
	// truncationMark 截断标注。
	truncationMark = "...(截断)"
)

// truncateContent 单条内容超过 maxContentRunes 字符时截断并标注。
func truncateContent(s string) string {
	runes := []rune(s)
	if len(runes) <= maxContentRunes {
		return s
	}
	return string(runes[:maxContentRunes]) + truncationMark
}

// formatChatLine 统一消息行格式：[MM-DD HH:MM] [来源] <发送者> 内容
func formatChatLine(ts time.Time, source, label, content string) string {
	return "[" + ts.Format(timeLayout) + "] " + source + " <" + label + "> " + content
}

// chatSource 来源标签：[群:928952016] / [私聊:3394859101]；未知类型原样展示。
func chatSource(chatType string, chatID int64) string {
	name := chatType
	switch chatType {
	case "group":
		name = "群"
	case "private":
		name = "私聊"
	}
	return "[" + name + ":" + strconv.FormatInt(chatID, 10) + "]"
}

// senderLabel 发送者标签：自己 → "我"；有昵称 → "昵称(QQ号)"；否则 QQ号。
func senderLabel(senderID int64, senderName string, isSelf bool) string {
	if isSelf {
		return "我"
	}
	id := strconv.FormatInt(senderID, 10)
	if senderName == "" {
		return id
	}
	return senderName + "(" + id + ")"
}

// Build 组装消息序列（spec P2 §2.2）：
//
//	system:  persona + agent格式说明 + 机器人自身QQ号 ← 全静态,缓存锚点
//	历史:    全会话混合的 user/assistant 按时间正序    ← 只追加,时间水印块裁剪
//	user:   [思考笔记] + 情绪 + 本轮新消息逐条 + 当前时间(首条头部) ← 每次变，放末尾
func (b *DefaultBuilder) Build(turn *Turn) []ChatMessage {
	// system 全静态（persona+guide+工具文档+账号QQ）：情绪等任何易变内容都
	// 不进 system，否则一次变化击穿整段缓存前缀（历史块 + 全部图片）。
	// 工具文档位于历史之前——历史每轮增长插不进它前面，文档每轮全命中；
	// 文档变更只发生在重启（registry 构造期生成），代价=一次全量 miss。
	system := b.persona + "\n" + b.guide
	if b.toolDocs != "" {
		system += "\n\n" + b.toolDocs
	}
	system += "\n当前账号QQ: " + strconv.FormatInt(turn.SelfID, 10)
	msgs := []ChatMessage{{Role: "system", Content: system}}
	b.appendHistory(&msgs, turn)
	msgs = b.appendNewMessages(msgs, turn)
	return msgs
}

// SetToolDocs 注入工具文档（ToolRegistry.PromptDocs 输出，启动时一次）。
func (b *DefaultBuilder) SetToolDocs(docs string) { b.toolDocs = docs }

// historyImageWindow 历史里允许走多模态转换的行窗口：只有从末尾数最后
// historyImageWindow 条历史行（无论是否带图，按行计数）才把图片转成
// image_url 部分，更早的带图行保持纯文本（文本已含 [图片] 标注）——旧图
// 的 rkey 多半已过期，逐条下载既拖慢组装又撑大上下文；要看更早的图由模型
// 调 view_message_images(message_id) 按需查看。
const historyImageWindow = 5

// appendHistory 查询并追加跨会话混合的历史消息（不按 chat 过滤，全局按时间
// 取最近 N 条），按时间正序（旧→新）。默认排除已撤回；查询失败时告警，
// 本轮不带历史继续对话。
func (b *DefaultBuilder) appendHistory(msgs *[]ChatMessage, turn *Turn) {
	if b.store == nil {
		return
	}
	records, err := b.store.QueryMessages(storage.MessageFilter{
		Limit: b.max + b.trim,
	})
	if err != nil {
		slog.Warn("查询历史消息失败，本轮不带历史上下文", "err", err)
		return
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 { // 倒序 → 正序
		records[i], records[j] = records[j], records[i]
	}
	// 块裁剪（时间水印锚）：保证两次调用间窗口头部字节级一致 → 命中 prompt cache
	records = b.evictStable(records)
	for i, r := range records {
		isSelf := r.Direction == "sent"
		role := "user"
		if isSelf {
			role = "assistant"
		}
		line := formatChatLine(time.Unix(int64(r.Time), 0), chatSource(r.ChatType, r.ChatID),
			senderLabel(r.UserID, r.SenderName, isSelf), truncateContent(r.RawMessage))
		var content any = line
		// P2.7 多模态：仅 user 行（收到的消息）携带图片内容；assistant 行保持
		// 纯文本（OpenAI 兼容 API 不接受 assistant 消息里的 image_url）。
		// 无图行保持纯 string，wire 字节不变（缓存前缀稳定）。
		// 图片按需上传策略：只有从末尾数最后 historyImageWindow 条行（无论
		// 是否带图，按行计数）才多模态化；更早的带图行保持纯文本，但同样把
		// [CQ:image,...] 换成 [图片] 标注（模型据此知道该行带过图）。
		if b.multimodal && role == "user" {
			if refs := extractImageRefs(r.Segments); len(refs) > 0 {
				text := replaceImageCQ(line)
				if len(records)-i <= historyImageWindow {
					content = b.multimodalContent(r.MessageID, text, refs)
				} else {
					content = text
				}
			}
		}
		*msgs = append(*msgs, ChatMessage{Role: role, Content: content})
	}
}

// evictStable 以时间水印为锚的块裁剪：窗口 = 时间 ≥ anchorTime 的行，仅在
// 窗口超过 max 时整块推进 trim（新锚 = 保留段最早一行的时间）。两次推进之间
// 窗口头部不变，历史段前缀字节级稳定。按相对条数裁剪在查询被 Limit 截断后
// （库中消息 > max+trim）会退化为逐条滑窗，每条新消息都换头击穿缓存，故锚定
// 绝对时间。同秒边界整组进/出窗：锚按行时间取值，若切在同秒组中间，下一轮
// 更早的同秒行会重新进窗换头。并发安全：锚只单调推进，并发 worker 最多损失
// 一次命中，不会读到回退的锚。
func (b *DefaultBuilder) evictStable(records []*storage.MessageRecord) []*storage.MessageRecord {
	if b.max <= 0 || b.trim <= 0 || len(records) == 0 {
		return records
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	window := records
	if b.anchorTime > 0 {
		i := 0
		for i < len(window) && window[i].Time < b.anchorTime {
			i++
		}
		window = window[i:]
	}
	if len(window) <= b.max {
		return window
	}
	drop := (len(window) - b.max + b.trim - 1) / b.trim * b.trim
	if drop > len(window) {
		drop = len(window)
	}
	for drop > 0 && window[drop-1].Time == window[drop].Time {
		drop--
	}
	b.anchorTime = window[drop].Time
	return window[drop:]
}

// appendNewMessages 把本轮新消息以 user 角色逐条追加在末尾（保留原始节奏，
// 不做文本拼接），行格式与历史一致（含来源标签）；首条新消息头部依次为
// [思考笔记] 块、当前情绪状态、当前时间——全部放末尾不进 system，否则缓存
// 前缀随笔记/情绪/时间失效。
// multimodal 开启时，带 Images 的新消息行同样多模态化（P2.7）。
func (b *DefaultBuilder) appendNewMessages(msgs []ChatMessage, turn *Turn) []ChatMessage {
	for i, m := range turn.NewMessages {
		content := formatChatLine(m.Time, chatSource(m.ChatType, m.ChatID),
			senderLabel(m.SenderID, m.SenderName, m.IsSelf), truncateContent(m.Text))
		if i == 0 {
			content = "当前时间: " + turn.Now.Format(nowLayout) + "\n" + content
			if turn.Mood != "" {
				content = "当前情绪状态: " + turn.Mood + "\n" + content
			}
			if len(turn.Notes) > 0 {
				content = FormatNotes(turn.Notes) + "\n\n" + content
			}
		}
		var c any = content
		if b.multimodal && len(m.Images) > 0 {
			c = b.multimodalContent(m.MessageID, content, m.Images)
		}
		msgs = append(msgs, ChatMessage{Role: "user", Content: c})
	}
	return msgs
}

// maxImagesPerMessage 单条历史消息最多携带的图片数（防上下文体积爆炸）。
const maxImagesPerMessage = 4

// extractImageRefs 解析消息段 JSON，按 image 段顺序取（Idx, URL）。Idx 对该
// 消息内全部 image 段计数（含缺 url 的段），与 storage.message_images 映射
// 锚保持一致；URL 为空的段保留占位（multimodalContent 会跳过）。最多取
// maxImagesPerMessage 个；输入为空或非法 JSON 返回 nil。
func extractImageRefs(segmentsJSON []byte) []imageRef {
	if len(segmentsJSON) == 0 {
		return nil
	}
	var segs []struct {
		Type string `json:"type"`
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(segmentsJSON, &segs); err != nil {
		return nil
	}
	var refs []imageRef
	idx := 0
	for _, seg := range segs {
		if seg.Type != "image" {
			continue
		}
		if len(refs) >= maxImagesPerMessage {
			break
		}
		refs = append(refs, imageRef{Idx: idx, URL: seg.Data.URL})
		idx++
	}
	return refs
}

// cqImageRe 匹配 [CQ:image,...] 码（值内的逗号转义为 &#44;，不含 ]）。
var cqImageRe = regexp.MustCompile(`\[CQ:image,[^\]]*\]`)

// replaceImageCQ 把文本里的 [CQ:image,...] 码替换为 [图片] 标注（其余 CQ 码原样）。
func replaceImageCQ(s string) string {
	return cqImageRe.ReplaceAllString(s, "[图片]")
}

// imagePart 构造一个 image_url 内容部分（OpenAI 多模态格式）。
func imagePart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &struct {
		URL string `json:"url"`
	}{URL: url}}
}

// multimodalContent 把行文本与图片段组装成 OpenAI 多模态内容数组：首项为
// 文本，其后每张可解析的图一项 image_url。图片经 imageStore 以
// (messageID, idx) 为锚解析——映射/磁盘命中直接读盘，未命中才按 URL 下载
// （QQ 临时链可能已失效）；解析失败的图片直接剔除（文本行已带 [图片] 标注）
// ——失效链接回传会拖垮整个请求（P3-3 修复前实测）。
func (b *DefaultBuilder) multimodalContent(messageID int64, text string, refs []imageRef) []ContentPart {
	parts := make([]ContentPart, 0, len(refs)+1)
	parts = append(parts, ContentPart{Type: "text", Text: text})
	for _, ref := range refs {
		du := b.images.dataURLFor(messageID, ref.Idx, ref.URL)
		if !strings.HasPrefix(du, "data:") { // 下载失败回退原 url，剔除该图
			continue
		}
		parts = append(parts, imagePart(du))
	}
	return parts
}
