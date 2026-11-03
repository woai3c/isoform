package engagement

// ActionMuteKind turns off future messages of the same kind.
const ActionMuteKind = "mute_kind"

func MuteKindLabel(lang string) string {
	if normalizeLang(lang) == "zh-CN" {
		return "静音此类消息"
	}
	return "Mute this kind"
}
