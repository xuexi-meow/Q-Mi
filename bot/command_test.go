package bot

import "testing"

func TestMatchCommand(t *testing.T) {
	RegisterCommand(Command{Name: "状态", Prefixed: true})
	RegisterCommand(Command{Name: "ping", Prefixed: false})
	RegisterCommand(Command{Name: "帮助", Prefixed: false})

	cases := []struct {
		text    string
		want    string
		args    string
		matched bool
	}{
		{"/状态", "状态", "", true},
		{"/状态 详细", "状态", "详细", true},
		{"状态", "", "", false},          // Prefixed 命令必须带前缀
		{"/ping", "", "", false},        // 裸词命令不认前缀
		{"  ping  ", "ping", "", true},  // 首尾空白剥离
		{"ping 参数", "ping", "参数", true}, // 参数保留原文,不做二次规整
		{"帮助", "帮助", "", true},
		{"/未注册", "", "", false},
		{"随便聊聊", "", "", false},
		{"pinging", "", "", false}, // 前缀匹配不算命中
		{"", "", "", false},
		{"/", "", "", false},
	}
	for _, tc := range cases {
		cmd, args := MatchCommand(tc.text)
		if tc.matched {
			if cmd == nil || cmd.Name != tc.want || args != tc.args {
				t.Errorf("MatchCommand(%q) = %v, %q; want %q, %q", tc.text, cmd, args, tc.want, tc.args)
			}
		} else if cmd != nil {
			t.Errorf("MatchCommand(%q) 不应命中, 得到 %+v", tc.text, cmd)
		}
	}
}

func TestRegisterCommandDuplicatePanics(t *testing.T) {
	RegisterCommand(Command{Name: "重复命令"})
	defer func() {
		if recover() == nil {
			t.Error("重复注册应 panic")
		}
	}()
	RegisterCommand(Command{Name: "重复命令"})
}
