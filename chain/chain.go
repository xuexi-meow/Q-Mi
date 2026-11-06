// Package chain 用于快速、直观地构建发送给 NapCat 的消息段链。
//
// 典型用法:
//
//	c := chain.New().
//	    Reply(strconv.FormatInt(e.MessageID, 10)).
//	    At("123456").
//	    Text(" 签到成功！").
//	    Image("E:/pics/sign.png")
//
//	request.SendGroupMsg(ctx, e.GroupID, c...)
//
// 带可选参数的复杂段(Image 的 summary、Music 的多参数等)通过 Raw 塞入:
//
//	c.Raw(apimsg.Image(file, apimsg.StringPtr("看图"), nil))
package chain

import (
	"encoding/base64"
	"log/slog"
	"os"
	"strings"

	apimsg "Q-Mi/api/message"
)

// Chain 消息段链,底层就是 OneBot 的段数组,可直接展开传给 request.SendGroupMsg 等。
type Chain []*apimsg.Message

// New 起一条新链,可顺带聚合现成的段。
func New(segs ...*apimsg.Message) Chain {
	return append(Chain{}, segs...)
}

// Append 把若干现成的段接到链尾。
func (c Chain) Append(segs ...*apimsg.Message) Chain {
	return append(c, segs...)
}

// Raw 逃生舱:塞入任意 apimsg 构造的段,用于带可选参数的复杂段。
func (c Chain) Raw(seg *apimsg.Message) Chain {
	return append(c, seg)
}

func (c Chain) Text(text string) Chain {
	return c.Append(apimsg.Text(text))
}

func (c Chain) At(qq string) Chain {
	return c.Append(apimsg.At(qq))
}

func (c Chain) AtAll() Chain {
	return c.Append(apimsg.At("all"))
}

func (c Chain) Reply(id string) Chain {
	return c.Append(apimsg.Reply(id))
}

func (c Chain) Face(id string) Chain {
	return c.Append(apimsg.Face(id))
}

func (c Chain) Dice(result string) Chain {
	return c.Append(apimsg.Dice(result))
}

func (c Chain) RPS(result string) Chain {
	return c.Append(apimsg.RPS(result))
}

func (c Chain) Poke(typ, id string) Chain {
	return c.Append(apimsg.Poke(typ, id))
}

func (c Chain) Json(data any) Chain {
	return c.Append(apimsg.Json(data))
}

func (c Chain) Forward(id string) Chain {
	return c.Append(apimsg.Forward(id, nil))
}

func (c Chain) MFace(emojiID, emojiPackageID string) Chain {
	return c.Append(apimsg.MFace(emojiID, emojiPackageID, nil, nil))
}

// Image 本地路径自动读文件转 base64://,url/base64:///file:// 原样透传。
func (c Chain) Image(file string) Chain {
	return c.Append(apimsg.Image(resolveFile(file), nil, nil))
}

// Record 同 Image 的文件规则。
func (c Chain) Record(file string) Chain {
	return c.Append(apimsg.Record(resolveFile(file)))
}

// Video 同 Image 的文件规则。
func (c Chain) Video(file string) Chain {
	return c.Append(apimsg.Video(resolveFile(file), nil))
}

// File 同 Image 的文件规则。
func (c Chain) File(file string) Chain {
	return c.Append(apimsg.FileMsg(resolveFile(file), nil))
}

// resolveFile 本地路径读出内容转 base64://;远程与已编码的形式原样透传。
// 读取失败不中断链:告警后透传原值,让 NapCat 侧报出明确错误。
func resolveFile(v string) string {
	switch {
	case strings.HasPrefix(v, "http://"),
		strings.HasPrefix(v, "https://"),
		strings.HasPrefix(v, "base64://"),
		strings.HasPrefix(v, "file://"):
		return v
	}
	data, err := os.ReadFile(v)
	if err != nil {
		slog.Warn("chain 读取本地文件失败,原样透传", "file", v, "err", err)
		return v
	}
	return "base64://" + base64.StdEncoding.EncodeToString(data)
}
