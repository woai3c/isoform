package topics

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// People's names must never become a topic: a label is sent to a web search.
// The people the user knows are passed in (Options.Names); these lists catch
// the rest the common way names show up in titles: a capitalized given name
// ("Priya Raman - LinkedIn", "Prep for Priya") and, in titles, a short Han
// run that starts with a common surname ("张伟 的主页"). Words that are also
// the names of tools (Ruby, Julia, Ada) are left off the list.
var givenNames = func() map[string]bool {
	names := strings.Fields(`
aaron abby abdul abigail adam aditi aditya adrian ahmed aisha alan albert alejandro alex alexander alexandra alexis
ali alice alicia alison amanda amelia amir amit amy ana andrea andrew andy angela anil anita ankit ann anna anne
anthony anton antonio arjun arthur arun ashley aya ayesha barbara ben benjamin beth bilal bob bonnie brandon brenda
brian bruce bryan caleb camila carl carla carlos carmen carol caroline catherine charles charlie charlotte chen chloe
chris christian christina christine christopher claire clara cody colin connor craig cristina cynthia daniel danielle
david deborah deepak denise dennis derek diana diego dmitri donald donna doug douglas dylan ed edward elena eli elijah
elizabeth ella ellen emily emma eric erica erik ethan eva evan fatima felix fernando fiona francesca francisco gabriel
gabriela gary george gerald gina giovanni giulia grace greg gregory hamza hannah harold harry hassan heather helen
henry hiroshi holly hugo ian igor ingrid irene isaac isabel isabella ivan jack jacob jake james jamie jan jane janet
jason javier jay jean jeff jeffrey jennifer jeremy jessica jim joan joe joel johan john jon jonathan jordan jorge jose
joseph joshua juan julian julie justin karen karim kate katherine kathryn katie kavya keith ken kenji kevin kim kumar
kyle laura lauren leah leo leon li liam lily linda lisa liu lucas lucia luis luke lynn madison mahmoud manuel marco
margaret maria marie marina mario mark martin mary matt matthew maya megan mei melissa mia michael michelle miguel mike
min mohamed mohammed mohammad monica muhammad nancy naomi natalie nathan neha nicholas nick nicole nikhil nina noah
noor oliver olivia omar oscar pablo pamela patricia patrick paul paula pedro peter philip pooja priya rachel rahul
raj rajesh ralph ram ramesh randy raul rebecca richard rick rita rob robert roberto robin rohan roger ronald rosa ross
roy russell ryan sai sam samantha samuel sandra sanjay sara sarah satoshi scott sean sergei shannon sharon shirley
simon sofia sophia sophie stephanie stephen steve steven sudhir sunil susan takeshi tanya taylor teresa thomas tim
timothy tina todd tom tony tyler valentina vanessa victor victoria vijay vikram vincent virginia walter wang wei
william xin yan yang yasmin yuki yusuf zach zachary zhang zoe
`)
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}()

// hanSurnames are the most common Chinese family names.
const hanSurnames = "王李张刘陈杨黄赵吴周徐孙马朱胡郭何林罗郑梁谢宋唐许韩冯邓曹彭曾萧田董袁潘蒋蔡余杜叶苏魏吕丁沈姚卢姜崔钟谭陆汪范廖贾夏韦邹孟熊秦邱尹薛闫段雷侯龙史陶黎贺顾毛郝龚邵万钱严覃武戴莫孔汤程方施金江任高石白温傅葛柳康于齐聂阎童倪洪易季常包宁乔"

// hanCompoundSurnames are the two-character family names; a name with one
// is three or four characters long ("欧阳娜娜").
var hanCompoundSurnames = []string{"欧阳", "司马", "上官", "诸葛", "慕容", "司徒", "夏侯", "令狐", "尉迟", "公孙", "宇文", "皇甫"}

// hanCommonWords are everyday words that start with a family name ("周末",
// "黄金", "程序"): they are words, not names, so a topic keeps them. A word
// missing here only drops out of a label; a name missing from the surname
// lists would be sent.
var hanCommonWords = func() map[string]bool {
	words := strings.Fields(`
王国 王者 王牌 李子 张贴 刘海 陈列 陈述 陈旧 杨树 黄金 黄色 黄油 黄昏 黄瓜 吴语 周末 周报 周会 周期
周年 周边 周围 周到 周刊 周日 周一 周二 周三 周四 周五 周六 徐州 孙子 马上 马拉松 马路 马车 马克 马桶
马达 胡同 胡子 胡说 胡椒 何时 何必 何况 何处 林业 林地 罗马 罗盘 郑州 郑重 谢谢 谢意 宋朝 宋体 唐朝
唐诗 许多 许可 许可证 许久 韩国 韩语 韩文 曾经 萧条 田野 田地 董事 董事会 董事长 潘多拉 余额 余下 余地
杜绝 叶子 叶绿素 苏州 苏打 苏醒 丁香 沈阳 卢布 姜黄 钟表 钟头 钟点 陆地 陆续 陆军 汪洋 范围 范例 范式
范文 夏天 夏季 夏令营 孟买 熊猫 熊市 秦朝 段落 段位 雷达 雷达图 雷电 侯爵 龙头 龙卷风 史诗 史料 陶瓷
黎明 贺卡 贺词 顾客 顾问 顾虑 毛利 毛巾 毛病 万一 万能 万物 万岁 钱包 钱财 严格 严重 严肃 武器 武汉
武术 莫名 孔雀 汤圆 汤匙 程序 程序员 程度 方案 方法 方向 方面 方便 方式 方程 方法论 施工 施肥 金融
金额 金属 金牌 金色 金币 金字塔 江苏 江西 江湖 任务 任务栏 任何 任意 高效 高级 高铁 高速 高度 高考
高端 高手 高清 石油 石头 石墨烯 白板 白皮书 白色 白天 白银 温度 温度计 温暖 温柔
于是 康复 柳树 葛根 童年 童话 童装 洪水 洪流 易用 易懂 易经 季度 季节 季报 季后赛
常用 常见 常识 常规 常量 包装 包括 包含 包子 包邮 宁愿 宁静 宁波 齐全 齐心 乔迁 柳州
张量 张开 张望 张家界 高中 高校 高性能 高通 高等 高架 高楼 高压 高分 高温 高价 高层 高阶 高频 高保真
高尔夫 高并发 高可用 高精度 黄河 黄山 黄页 黄牛 田园 田径 金牛 金牛座 金价 金鱼 金丝雀 金句 石墨 石英
石膏 石榴 白酒 白菜 白领 白名单 白噪音 白宫 方块 方糖 方差 温泉 温室 万圣节 史上 龙虾 龙舟 龙井 戴尔 罗技
罗马尼亚 雷蛇 苏宁 苏格兰 夏威夷 马克杯 马卡龙 马铃薯 马赛克 任天堂 韩剧 韩流 武侠 钱塘江 严选 毛衣 毛笔
毛孔 贺岁 陶艺 段子 秦岭 姜汁 卢浮宫 杜比 宋代 谢幕 胡萝卜 胡桃 朱红 陈皮 陈年 易用性 常温 童装
`)
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}()

// hanNameLike reports a Han run written like a name: it starts with a
// common family name (a compound one then at least three characters long)
// and no everyday word begins it ("张量分解", "高中数学", "黄河" are words). A
// name fused with the words after it is still a name ("王伟新闻",
// "刘德华电影"): Han text has no spaces to end it. A word the list misses is
// refused with the names; that is the safer error.
func hanNameLike(key string) bool {
	runes := []rune(key)
	if len(runes) < 2 || hanCommonPrefix(key) {
		return false
	}
	for _, compound := range hanCompoundSurnames {
		if strings.HasPrefix(key, compound) {
			return len(runes) >= 3
		}
	}
	return strings.ContainsRune(hanSurnames, runes[0])
}

// hanCommonPrefix reports a run that starts with an everyday word.
func hanCommonPrefix(key string) bool {
	for word := range hanCommonWords {
		if strings.HasPrefix(key, word) {
			return true
		}
	}
	return false
}

// lowercaseNameWords are given names that are also everyday or tool words
// ("mark as read", "Adam optimizer", "Hugo themes"): only a capitalized
// spelling counts as a name for them.
var lowercaseNameWords = map[string]bool{
	"mark": true, "grace": true, "jack": true, "hugo": true, "adam": true, "victor": true,
	"rosa": true, "rita": true, "lily": true, "maya": true, "luke": true, "noah": true,
	"jordan": true, "ross": true,
}

// nameLike reports a token written like a person's name: a short Han run
// that starts with a family name, or a given name. In searches and page
// titles, where people look each other up ("sam altman", "mark zuckerberg"),
// every given name counts, in any case; holding back "mark as read" there
// is the safer mistake. Elsewhere a lower-case given name counts when it has
// four letters or more and is not also an everyday word.
func nameLike(tok token, source Source) bool {
	if hanToken(tok.key) {
		return hanNameLike(tok.key)
	}
	if !givenNames[tok.key] {
		return false
	}
	switch source {
	case SourceSearch, SourcePage:
		return true
	default:
	}
	first, _ := utf8.DecodeRuneInString(tok.surface)
	if unicode.IsUpper(first) {
		return true
	}
	return utf8.RuneCountInString(tok.key) >= 4 && !lowercaseNameWords[tok.key]
}
