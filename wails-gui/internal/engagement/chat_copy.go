package engagement

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/identity"
)

var chatDuration = regexp.MustCompile(`^(\d+) (minute|minutes|hour|hours)$`)

// ChatParams translates the small vocabulary emitted by producers. User titles
// and source text remain verbatim; no model or source lookup is involved.
func ChatParams(params map[string]string, lang string) map[string]string {
	out := CanonicalChatParams(params)
	if normalizeLang(lang) != "zh-CN" {
		return out
	}
	for key, value := range out {
		switch key {
		case "slot":
			switch value {
			case "morning":
				out[key] = "早间"
			case "evening":
				out[key] = "晚间"
			case "on-request":
				out[key] = "按需"
			}
		case "what":
			switch value {
			case "Screen capture":
				out[key] = "屏幕捕获"
			case "Notification access":
				out[key] = "通知访问"
			case "Notifications":
				out[key] = "通知"
			case "Microphone":
				out[key] = "麦克风"
			case "System audio":
				out[key] = "系统音频"
			}
		case "duration":
			if match := chatDuration.FindStringSubmatch(value); match != nil {
				unit := "分钟"
				if strings.HasPrefix(match[2], "hour") {
					unit = "小时"
				}
				out[key] = match[1] + " " + unit
			}
		case "place":
			for prefix, translated := range map[string]string{"on ": "通过", "at ": "在"} {
				if strings.HasPrefix(value, prefix) {
					out[key] = translated + strings.TrimPrefix(value, prefix)
					break
				}
			}
		case "where":
			if name, ok := identity.ChineseAppName(value); ok {
				out[key] = name
			}
		case "people":
			if strings.HasPrefix(value, "with ") {
				out[key] = "与" + strings.TrimPrefix(value, "with ")
			}
		case "summary":
			out[key] = chatCalendarSummary(value)
		case "result":
			out[key] = chatPlaybookResult(value, lang)
		case "due":
			switch value {
			case "today":
				out[key] = "今天"
			case "tomorrow":
				out[key] = "明天"
			case "yesterday":
				out[key] = "昨天"
			default:
				if date, err := time.Parse("on Jan 2", value); err == nil {
					out[key] = fmt.Sprintf("%d月%d日", date.Month(), date.Day())
				}
			}
		}
	}
	return out
}

// CanonicalChatParams reverses only producer vocabulary while recovering an
// older Chinese sentence. Arbitrary source text is never translated.
func CanonicalChatParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for key, value := range params {
		out[key] = value
		switch key {
		case "result":
			out[key] = chatPlaybookResult(value, "en")
		case "slot":
			switch value {
			case "早间":
				out[key] = "morning"
			case "晚间":
				out[key] = "evening"
			case "按需":
				out[key] = "on-request"
			case "", "unknown":
				out[key] = ""
			}
		case "what":
			switch value {
			case "屏幕捕获":
				out[key] = "Screen capture"
			case "通知访问":
				out[key] = "Notification access"
			case "通知":
				out[key] = "Notifications"
			case "麦克风":
				out[key] = "Microphone"
			case "系统音频":
				out[key] = "System audio"
			}
		case "duration":
			for suffix, unit := range map[string]string{"分钟": "minutes", "小时": "hours"} {
				if strings.HasSuffix(value, suffix) {
					number := strings.TrimSpace(strings.TrimSuffix(value, suffix))
					if _, err := strconv.Atoi(number); err == nil {
						if number == "1" {
							unit = strings.TrimSuffix(unit, "s")
						}
						out[key] = number + " " + unit
					}
				}
			}
		case "due":
			switch value {
			case "今天":
				out[key] = "today"
			case "明天":
				out[key] = "tomorrow"
			case "昨天":
				out[key] = "yesterday"
			default:
				if date, err := time.Parse("1月2日", value); err == nil {
					out[key] = date.Format("on Jan 2")
				}
			}
		}
	}
	return out
}

var chatOverlapSummary = regexp.MustCompile(`^(.*?) \(([^()]*)\) overlaps (.*?) \(([^()]*)\)\.$`)
var chatInviteSummary = regexp.MustCompile(`^You have not answered the invite to (.*?) \((Mon|Tue|Wed|Thu|Fri|Sat|Sun) (.*?)\)\.$`)

func chatCalendarSummary(value string) string {
	if parts := chatOverlapSummary.FindStringSubmatch(value); parts != nil {
		return Render("{first}（{firstTime}）与{second}（{secondTime}）时间重叠。", map[string]string{"first": parts[1], "firstTime": parts[2], "second": parts[3], "secondTime": parts[4]})
	}
	if parts := chatInviteSummary.FindStringSubmatch(value); parts != nil {
		days := map[string]string{"Mon": "周一", "Tue": "周二", "Wed": "周三", "Thu": "周四", "Fri": "周五", "Sat": "周六", "Sun": "周日"}
		return Render("你还没有回复{title}（{day} {time}）的邀请。", map[string]string{"title": parts[1], "day": days[parts[2]], "time": parts[3]})
	}
	return value
}

func chatPlaybookResult(value, lang string) string {
	for _, sourceLang := range Languages {
		if !strings.HasPrefix(value, playbookFailed[sourceLang]) {
			continue
		}
		reason := strings.TrimSpace(strings.TrimPrefix(value, playbookFailed[sourceLang]))
		switch reason {
		case playbookSeeWhy[sourceLang]:
			reason = ""
		case playbookChanged[sourceLang]:
			reason = PlaybookChangedReason(lang)
		}
		return PlaybookFailedResult(lang, reason)
	}
	return value
}
