package main

func appMenuTitles(language string) [3]string {
	switch normalizeUILanguage(language) {
	case "zh-CN":
		return [3]string{"关于 Isoform", "检查更新…", "设置…"}
	default:
		return [3]string{"About Isoform", "Check for updates…", "Settings…"}
	}
}

// dispatchAppMenuAction shares the native menu's navigation contract with tests.
func dispatchAppMenuAction(action int, open func(string), check func()) {
	switch action {
	case 0:
		open("about")
	case 1:
		open("about")
		check()
	case 2:
		open("")
	}
}
