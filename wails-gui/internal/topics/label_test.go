package topics

import (
	"strings"
	"testing"
)

func TestCheckLabel(t *testing.T) {
	names := []string{"yetone", "Claude Shannon", "张伟", "ana.lopez@example.com", "Alex @alex"}
	cases := []struct {
		text string
		want string
	}{
		{"wearable AI camera hats", ""},
		{"silent speech interface sensors", ""},
		{"SwiftUI", ""},
		{"ESP32-S3 wearable camera", ""},
		{"magpie yetone plugin system", LabelPerson},
		{"Priya resume tips", LabelPerson},
		{"priya resume tips", LabelPerson},
		{"Mark Zuckerberg AI glasses", LabelPerson},
		// "Claude" is a stop word, but here it is a known person's name.
		{"Claude information theory", LabelPerson},
		{"Shannon entropy coding", LabelPerson},
		{"ana lopez portfolio", LabelPerson},
		{"张伟的主页设计", LabelPerson},
		{"王芳 项目", LabelPerson},
		// A spaced known name, and unknown names fused with the words after.
		{"张 伟 新闻", LabelPerson},
		{"王伟新闻", LabelPerson},
		{"王伟的新闻", LabelPerson},
		{"欧阳娜娜演唱会", LabelPerson},
		{"最新关于王伟的报道", LabelPerson},
		// Everyday words that start with a family name stay.
		{"周末活动 推荐", ""},
		{"程序设计 入门", ""},
		{"金融科技 新闻", ""},
		{"张量分解 教程", ""},
		{"张量 计算", ""},
		{"高中数学 题库", ""},
		{"高性能计算 集群", ""},
		{"黄河 旅游", ""},
		{"田园生活 视频", ""},
		{"金牛座 运势", ""},
		{"高通骁龙 芯片", ""},
		// Two-character given names that are also words stay names, and a
		// name fused with any word after it is refused.
		{"高峰 讲座", LabelPerson},
		{"杨柳 画展", LabelPerson},
		{"张力 访谈", LabelPerson},
		{"何为 博客", LabelPerson},
		{"金山 演讲", LabelPerson},
		{"王子 视频", LabelPerson},
		{"张伟摄影", LabelPerson},
		{"刘德华电影", LabelPerson},
		{"https://example.com/a", LabelLink},
		{"notes/plan", LabelLink},
		{"@yetone plugins", LabelLink},
		{"hat", LabelGeneric},
		{"brief", LabelGeneric},
		{"the and of", LabelEmpty},
		// The domain of an address names no one.
		{"example com landing pages", ""},
		{"alex open source", LabelPerson},
	}
	for _, c := range cases {
		if got := CheckLabel(c.text, names); got != c.want {
			t.Errorf("CheckLabel(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

// Rank reads Han names the same way: fused with the words after them, or
// spelled with spaces.
func TestRankDropsFusedAndSpacedHanNames(t *testing.T) {
	var signals []Signal
	for _, text := range []string{"王伟新闻", "张 伟 新闻", "张伟的主页"} {
		for _, day := range []float64{1, 2, 3} {
			signals = append(signals, Signal{Source: SourceSearch, Text: text, At: daysAgo(day)})
		}
	}
	for _, label := range rankLabels(signals, Options{Names: []string{"张伟"}}) {
		for _, name := range []string{"王伟", "张伟", "张", "伟"} {
			if strings.Contains(label, name) {
				t.Fatalf("label %q carries a name", label)
			}
		}
	}
}
