package engagement

import "time"

// FirstWeekDay uses local calendar dates, including across daylight saving.
// The install date is the first completed onboarding, independent of telemetry.
func FirstWeekDay(installed, now time.Time) int {
	if installed.IsZero() || now.Before(installed) {
		return -1
	}
	local := installed.In(now.Location())
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(day.Sub(start) / (24 * time.Hour))
}

func NotForMeLabel(lang string) string {
	if normalizeLang(lang) == "zh-CN" {
		return "不适合我，静音"
	}
	return "Not for me, mute"
}

// FirstWeekCopy contains no observed names or titles. The source identity and
// real destination are carried separately by the chat card.
func FirstWeekCopy(stage, firstBrief, lang string) string {
	if normalizeLang(lang) == "zh-CN" {
		switch stage {
		case "welcome":
			timing := "第一份简报计划在 " + firstBrief + "，电脑开机且可用时提供。"
			if firstBrief == "off" {
				timing = "定时简报已关闭；你可以随时在 Today 请求第一份简报。"
			} else if firstBrief == "" {
				timing = "第一份简报会在电脑可用时提供；你也可以随时在 Today 请求简报。"
			}
			return "你好，我是 Isoform。我会根据你允许读取的内容，留意承诺、人物和会议笔记。" + timing + "你可以在电脑和手机的聊天中找到我。第一周我每天最多主动提一件事；任何对外操作都需要你的许可。"
		case "brief":
			return "这是你的第一份简报：把已获准读取的事项整理成今天需要你关注的内容。打开简报看看；我看不到的部分会明确说明。"
		case "commitment":
			return "我在你的 Purpose Map 中看到一项未完成的承诺。打开 Today 核对下一步；如果这类提醒不适合你，可以静音。"
		case "person":
			return "最近的记录中出现了一位你认识的人。打开人物列表查看已有背景，决定是否需要跟进。"
		case "meeting":
			return "一份会议笔记已经准备好。打开笔记核对决定和后续事项；我不会把推测当作你答应的事。"
		}
		return ""
	}
	switch stage {
	case "welcome":
		timing := "Your first brief is planned for " + firstBrief + ", when your computer is available."
		if firstBrief == "off" {
			timing = "Scheduled briefs are off; request your first from Today."
		} else if firstBrief == "" {
			timing = "Your first brief will be available when your computer is ready; you can also request one from Today."
		}
		return "Hi, I'm Isoform. I'll notice commitments, people and meeting notes in sources you allow. " + timing + " Find me in the chat on your computer and phone. This week I'll raise at most one thing daily. Any outside action needs your permission."
	case "brief":
		return "This is your first brief: a short view of what needs your attention today, drawn from the sources you allow. Open it to look ahead; I'll say where my view is incomplete."
	case "commitment":
		return "I noticed an open commitment in your Purpose Map. Open Today to check its next step. If this kind of check-in isn't useful, you can mute it."
	case "person":
		return "Someone you know appeared in your recent records. Open the People list to review the context and decide whether a follow-up would help."
	case "meeting":
		return "A meeting note is ready. Open the notes to check the decisions and follow-ups. I'll keep an observation separate from a promise you've actually made."
	}
	return ""
}
