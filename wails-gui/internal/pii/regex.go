package pii

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/platform/ocr"
)

const (
	regexLabelValueSep     = `\s*(?:[:：=]|#|为|是|は|が)?\s*`
	regexStructuredValue   = `([\p{L}\p{N}][\p{L}\p{N} _./#:\-ー－]{2,40})`
	regexOrganizationValue = `([\p{L}\p{N}][\p{L}\p{N} _.'’·・ー－＆&（）(),-]{1,60})`

	regexInsuranceOrgLabels = `(?i)(?:insurance\s+(?:company|carrier|provider|agency)|insurer|underwriter|assureur|compagnie\s+d['’]assurance|courtier\s+d['’]assurance|versicherer|versicherungsgesellschaft|aseguradora|compa(?:ñ|n)[íi]a\s+de\s+seguros|seguradora|companhia\s+de\s+seguros|compagnia\s+assicurativa|assicuratore|assicurazione|verzekeraar|verzekeringsmaatschappij|保险公司|保險公司|承保公司|保险人|保險人|保険会社名?|引受保険会社|幹事保険会社|保険者|取扱代理店|보험사|보험회사|보험자)`
	regexPolicyLabels       = `(?i)(?:policy(?:\s*(?:no\.?|number|id))?|insurance(?:\s*(?:policy|contract))?(?:\s*(?:no\.?|number|id))?|claim(?:\s*(?:no\.?|number|id))?|(?:account|acct)(?:\s*(?:no\.?|number|id))?|contract(?:\s*(?:no\.?|number|id))?|member(?:\s*(?:id|number))?|保单(?:号|号码|编号)?|保單(?:號|號碼|編號)?|保险(?:合同|单号|编号)?|保險(?:合同|單號|編號)?|理赔(?:号|号码|编号)?|理賠(?:號|號碼|編號)?|保険(?:証券|契約)?(?:番号)?|証券(?:番号)?|契約(?:番号)?|契約者(?:番号)?|加入者(?:番号)?|会員(?:番号)?|보험(?:증권|계약)?(?:번호)?|증권번호|계약번호|회원번호|num(?:é|e)ro\s+de\s+(?:police|contrat|sinistre|compte)|contrat\s+d['’]assurance|versicherungsnummer|policennummer|vertragsnummer|schadennummer|n(?:ú|u)mero\s+de\s+(?:p(?:ó|o)liza|contrato|siniestro|cuenta)|contrato\s+de\s+seguro|n(?:ú|u)mero\s+da\s+ap(?:ó|o)lice|ap(?:ó|o)lice|contrato\s+de\s+seguro|sinistro|numero\s+di\s+polizza|contratto\s+assicurativo|sinistro|polisnummer|verzekeringsnummer|schadenummer)`
	regexVehicleLabels      = `(?i)(?:license\s*plate|number\s*plate|vehicle\s*registration|registration\s*(?:no\.?|number)|plate\s*(?:no\.?|number)|\bvin\b|vehicle\s*identification\s*number|chassis(?:\s*(?:no\.?|number))?|車両番号|登録番号|自動車登録番号|ナンバープレート|車両ナンバー|車台番号|車体番号|车牌(?:号|号码)?|車牌(?:號|號碼)?|牌照(?:号|号码|號|號碼)?|车辆登记号|車輛登記號|车架号|車架號|车辆识别代码|車輛識別代碼|차량번호|자동차등록번호|번호판|차대번호|plaque\s+d['’]immatriculation|num(?:é|e)ro\s+d['’]immatriculation|kennzeichen|kfz-kennzeichen|nummernschild|fahrgestellnummer|matr(?:í|i)cula|placa|n(?:ú|u)mero\s+de\s+(?:placa|bastidor)|chassi|targa|numero\s+di\s+targa|telaio|kenteken|chassisnummer)`
	regexGovernmentIDLabels = `(?i)(?:national\s*id|identity\s*(?:card|number)|id\s*card\s*(?:no\.?|number)?|passport\s*(?:no\.?|number)|driver'?s?\s*licen[cs]e\s*(?:no\.?|number)?|tax\s*id|taxpayer\s*id|social\s*security\s*(?:no\.?|number)|\b(?:ssn|sin|nino|dni|nie|nif|cpf|aadhaar|pan|bsn)\b|ni\s*number|resident\s*(?:card|registration)\s*(?:no\.?|number)?|身份证(?:号|号码)?|身份證(?:號|號碼)?|身分證(?:號|號碼)?|护照(?:号|号码)?|護照(?:號|號碼)?|驾驶证(?:号|号码)?|駕照(?:號|號碼)?|マイナンバー|個人番号|免許証番号|旅券番号|在留カード番号|健康保険証番号|주민등록번호|외국인등록번호|여권번호|운전면허번호|num(?:é|e)ro\s+de\s+s(?:é|e)curit(?:é|e)\s+sociale|carte\s+d['’]identit(?:é|e)|passeport|personalausweisnummer|reisepassnummer|steueridentifikationsnummer|sozialversicherungsnummer|pasaporte|codice\s+fiscale|carta\s+d['’]identit(?:à|a)|passaporto)`
)

// regexPatterns defines regex-based PII detection patterns.
// These supplement the GLiNER detector and cover obvious structured values.
var regexPatterns = []regexPattern{
	{
		category: "private_email",
		re:       regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`),
	},
	{
		category: "private_email",
		// OCR may insert spaces around dots and @ when an email is split into
		// multiple text observations, e.g. "alex . ops @ example . com".
		re: regexp.MustCompile(`(?i)\b[A-Z0-9_%+\-]+(?:\s*\.\s*[A-Z0-9_%+\-]+)*\s*@\s*[A-Z0-9\-]+(?:\s*\.\s*[A-Z0-9\-]+)+\b`),
	},
	{
		category: "private_phone",
		// Context-bound phone numbers. Keeping a nearby phone/call label avoids
		// treating arbitrary dates, prices, or issue IDs as phone numbers.
		re:       regexp.MustCompile(`(?i)(?:phone|tel|mobile|cell|call|contact|reachable|reach|text|sms|联系电话|电话|手机号|手机|電話|電話番号|連絡先|携帯)(?:\s+(?:me|us|support|customer|user|on|file|at|back|before|after|via|for|number|no\.?|is|to)){0,4}\s*(?:[:：=]|\bat\b)?\s*(\+?\d[\d\s().\-ー－]{6,}\d)\b`),
		validate: looksLikePhoneNumber,
		extract:  1,
	},
	{
		category: "credit_card",
		// 13-19 digit sequences with optional spaces/dashes (Visa, MC, Amex, etc.)
		re:       regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`),
		validate: luhnCheck,
	},
	{
		category: "credit_card",
		// 3+ groups of 4 digits (common card display format, catches partial OCR splits)
		re: regexp.MustCompile(`\b\d{4}[\s-]+\d{4}[\s-]+\d{4}(?:[\s-]+\d{1,4})?\b`),
	},
	{
		category: "cvv",
		// 3-4 digit code near CVV/CVC/security code labels
		re:      regexp.MustCompile(`(?i)(?:cvv|cvc|cvv2|cvc2|security\s*code|verification|验证码)\s*[:\s]*(\d{3,4})\b`),
		extract: 1, // capture group 1
	},
	{
		category: "one_time_password",
		// OTP/TOTP: 6-digit codes near explicit labels, including filename
		// separators in the label, value separator and grouped digits.
		re:      regexp.MustCompile(`(?i)(?:otp|totp|one[-_ ]?time(?:[\s_-]*password)?|2fa(?:[\s_-]*code)?|mfa(?:[\s_-]*code)?|auth(?:entication|enticator)?(?:[\s_-]*code)?|verification[\s_-]*code|一次性密码|动态密码)[\s:：_-]*(\d{3}[\s.,_-]*\d{3})(?:\b|_)`),
		extract: 1,
	},
	{
		category: "password",
		// Password value after an explicit delimiter (: or =).
		// Must not start with / - . to avoid matching file paths and flags.
		re:      regexp.MustCompile(`(?i)(?:password|passwd|密码|口令)\s*[:=]\s*([^\s/\-.][^\s]{5,})`),
		extract: 1,
	},
	{
		category: "password",
		// OCR and code editors often preserve the password label but insert
		// spaces around symbols, or include quotes before the value. Keep this
		// delimiter-bound so natural prose mentioning passwords is not masked.
		re:      regexp.MustCompile(`(?i)(?:password|passwd|db[_ -]?password|database[_ -]?password|密码|口令)\s*[:=]\s*["']?([^\s,;]{4,40}(?:\s*[!@#$%^&*()_+=|:<>.?~/-]\s*[^\s,;]*){0,4})`),
		extract: 1,
	},
	{
		category: "password",
		// Credentials embedded in DSNs/URLs, e.g.
		// postgres://app:P@ssw0rd!DB@db:5432/app.
		re:      regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\r\n:@/]+:\s*([^\r\n/?]{6,80})@[\p{L}\p{N}.-]+(?::\d+)?(?:[/\s]|$)`),
		extract: 1,
	},
	{
		category: "street_address",
		// US-style street address: number + street name + suffix, optionally followed by city/state/zip.
		re: regexp.MustCompile(`\b\d{1,6}\s+[A-Z][a-zA-Z]+\s+(?:St|Ave|Rd|Blvd|Dr|Ln|Ct|Way|Pl|Cir|Pkwy|Ter|Loop|Trail|Hwy)\b[^"\n]{0,60}?\b\d{5}\b`),
	},
	{
		category: "private_address",
		// Japanese postal codes, only when explicitly labeled or prefixed by the postal mark.
		re:      regexp.MustCompile(`(?:郵便番号|〒)\s*[:：=]?\s*(\d{3}[-ー－]?\d{4})\b`),
		extract: 1,
	},
	{
		category: "government_id",
		// Government identifiers are context-bound to avoid masking arbitrary numeric strings.
		re:      regexp.MustCompile(regexGovernmentIDLabels + regexLabelValueSep + regexStructuredValue),
		extract: 1,
	},
	{
		category: "private_org",
		// Insurance company names become sensitive in insurance-policy context.
		re:      regexp.MustCompile(regexInsuranceOrgLabels + regexLabelValueSep + regexOrganizationValue),
		extract: 1,
	},
	{
		category: "license_plate",
		// Japanese vehicle registration plate, e.g. 品川 300 あ 12-34 or 横浜530さ5678.
		re: regexp.MustCompile(`[\p{Han}\p{Hiragana}]{1,5}\s*\d{2,3}\s*[ぁ-ん]\s*(?:\d{1,2}[-ー－]?\d{2}|\d{4})\b`),
	},
	{
		category: "license_plate",
		// Mainland China plate shape. Constrained to province abbreviations to keep false positives low.
		re: regexp.MustCompile(`(?:京|津|沪|滬|渝|冀|豫|云|雲|辽|遼|黑|湘|皖|鲁|魯|新|苏|蘇|浙|赣|贛|鄂|桂|甘|晋|晉|蒙|陕|陝|吉|闽|閩|贵|貴|粤|粵|青|藏|川|宁|寧|琼|瓊)[A-Z][A-Z0-9]{5,6}\b`),
	},
	{
		category: "license_plate",
		// Korean vehicle plate shape, e.g. 12가 3456 or 123가4567.
		re: regexp.MustCompile(`\b\d{2,3}[가-힣]\s?\d{4}\b`),
	},
	{
		category: "license_plate",
		// Vehicle plate/registration number next to explicit multilingual labels. This catches noisy OCR variants that
		// do not match the full Japanese plate shape but are still vehicle-specific identifiers.
		re:      regexp.MustCompile(regexVehicleLabels + regexLabelValueSep + regexStructuredValue),
		extract: 1,
	},
	{
		category: "account_number",
		// Policy/account/vehicle identifiers next to explicit labels. This
		// catches common insurance and account-number phrasing across major
		// languages without matching bare-label prose. Keep existing structured
		// formats without cutting long values. A first group can have one or
		// two digits; subsequent numeric or uppercase code groups belong to
		// the value, while following prose stays outside it.
		re:       regexp.MustCompile(`(?:^|[^\p{Latin}\p{N}])(` + regexPolicyLabels + `)` + regexLabelValueSep + `([\p{L}\p{N}][\p{L}\p{N}_./#:\-ー－]*(?:[ \t]+(?:(?-i:[A-Z]+)[ \t]+)?[0-9][\p{L}\p{N}_./#:\-ー－]*)*)`),
		extract:  2,
		validate: accountValuePresent,
	},
	{
		category: "account_number",
		// Numeric values also need a boundary before sentence punctuation.
		// Match complete groups rather than falling back to their first token;
		// the minimum digit count applies across the whole value.
		re:       regexp.MustCompile(`(?:^|[^\p{Latin}\p{N}])(` + regexPolicyLabels + `)` + regexLabelValueSep + `([0-9]+(?:[ \t]+[0-9]+)*)`),
		extract:  2,
		validate: accountNumericValuePresent,
	},
	{
		category: "api_key",
		// API keys / secret tokens with common prefixes, including after an
		// underscore that separates words in a filename.
		// Short minimum (3+) for sk-/glpat- so OCR line splits like "sk-or-v1-"
		// still match; continuation detection handles subsequent lines.
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9])(sk-[a-zA-Z0-9_-]{3,}|ghp_[a-zA-Z0-9]{3,}|gho_[a-zA-Z0-9]{3,}|glpat-[a-zA-Z0-9_-]{3,}|xoxb-[a-zA-Z0-9-]{3,}|xoxp-[a-zA-Z0-9-]{3,}|AKIA[0-9A-Z]{16}|AGE-SECRET-KEY-[A-Z0-9]+)`),
		extract: 1,
	},
	{
		category: "api_key",
		// Long hex strings (30+ chars) that fill an entire OCR observation -- likely
		// a token, hash, or key fragment. Only matches when the hex string is the
		// dominant content of the observation (anchored with ^ and $).
		re: regexp.MustCompile(`^[0-9a-f]{30,}$`),
	},
	{
		category: "api_key",
		// Generic key=value patterns: api_key, secret_key, access_token, etc.
		re:      regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|private[_-]?key)\s*[:=]\s*["']?([a-zA-Z0-9_\-/.]{20,})["']?`),
		extract: 1,
	},
	{
		category: "session_id",
		// Session/cookie identifiers only when labeled. These are not always PII, but they are sensitive
		// enough for Yansu's stored activity text and screenshots.
		re:      regexp.MustCompile(`(?i)(?:session[_ -]?(?:id|secret)|cookie[_ -]?id|sid|refresh[_ -]?token|reset[_ -]?token)\s*[:=_-]\s*["']?([a-zA-Z0-9][a-zA-Z0-9_\-./]{11,})["']?`),
		extract: 1,
	},
	{
		category: "session_id",
		// OCR sometimes fuses token labels and values (e.g.
		// "reset_tokenert_live_..."). Treat long fused labeled values as
		// sensitive so the containing OCR box is still masked.
		re:      regexp.MustCompile(`(?i)(?:session[_ -]?(?:id|secret)|refresh[_ -]?token|reset[_ -]?token)["']?([a-zA-Z0-9][a-zA-Z0-9_\-./]{11,})`),
		extract: 1,
	},
	{
		category: "device_id",
		// Device identifiers with explicit labels. IMEI/IMSI are 14-16 digits; UUID-style IDs are also common.
		re:      regexp.MustCompile(`(?i)(?:imei|imsi|device[_ -]?id|advertising[_ -]?id|idfa|gaid)\s*[:：=]\s*["']?([a-zA-Z0-9][a-zA-Z0-9_\-]{7,35})["']?`),
		extract: 1,
	},
	{
		category: "private_link",
		// Signed/shared URLs with high-confidence authentication tokens in query
		// parameters. Avoid short generic params such as sp/se/sv because they
		// are common in non-sensitive URLs.
		re: regexp.MustCompile(`(?i)https?://[^\s"'<>]{10,}[?&](?:sig|signature|x-amz-signature|x-amz-credential|x-goog-signature|x-goog-credential|token|access_token|auth)=[^\s"'<>&]{8,}`),
	},
	{
		category: "private_link",
		// Azure SAS URLs use short params such as sv/se/sp, but the reliable
		// sensitive signal is a signature on an Azure Storage host.
		re: regexp.MustCompile(`(?i)https?://[^\s"'<>]*(?:blob|dfs|file|queue)\.core\.windows\.net/[^\s"'<>]*\?(?:sig=|[^\s"'<>]*&sig=)[^\s"'<>&]{8,}`),
	},
	{
		category: "private_link",
		// Cloud file-sharing URLs with unique share IDs (Dropbox, Google Drive, OneDrive, etc.).
		re: regexp.MustCompile(`https?://(?:(?:drive|docs)\.google\.com/(?:file|document|spreadsheets|presentation)/d/[a-zA-Z0-9_-]{20,}|(?:www\.)?dropbox\.com/s(?:cl)?/[a-z0-9]{15,}|1drv\.ms/[a-zA-Z0-9]{5,}|onedrive\.live\.com/[^\s"'<>]{20,})`),
	},
}

type regexPattern struct {
	category string
	re       *regexp.Regexp
	validate func(string) bool // optional validation (e.g. Luhn for CC)
	extract  int               // submatch index to use (0 = full match)
}

// DetectWithRegex scans OCR observations for PII using regex patterns.
// Returns additional detections that complement the GLiNER model.
func DetectWithRegex(observations []ocr.TextObservation) []DetectionResult {
	var results []DetectionResult

	// Pass 1: scan individual observations.
	for i, obs := range observations {
		hits := matchPatterns(obs.Text, obs)
		results = append(results, hits...)

		// Pass 1b: continuation detection.
		// If a match reaches the end of the observation text, the value likely
		// wraps to the next line(s). Mask subsequent observations that appear
		// directly below (similar X band, next Y) as continuation lines.
		for _, h := range hits {
			text := strings.TrimSpace(obs.Text)
			if matchReachesEnd(h.Text, text) {
				results = append(results, findContinuationLines(observations, i, h.Category)...)
			}
		}
	}

	// Pass 2: merge adjacent observations into lines and re-scan.
	// OCR often splits a single line (e.g. a credit card number) into
	// multiple observations. We group observations that share a similar
	// Y coordinate (within tolerance) and concatenate their text. A match
	// is masked with the boxes of the observations it covers, not the
	// whole row: one tall member (a large heading, vertical text, an OCR
	// block) would otherwise stretch the row's box past the mask size cap
	// and cost the whole frame.
	lines := mergeObservationsIntoLines(observations)
	for _, line := range lines {
		for _, m := range matchPatternRanges(line.Text) {
			results = append(results, line.memberRegions(m)...)
		}
	}

	return deduplicateDetections(results)
}

// matchReachesEnd returns true if the matched text appears at (or very near)
// the end of the full observation text, suggesting truncation / line wrap.
func matchReachesEnd(matched, fullText string) bool {
	idx := strings.LastIndex(fullText, matched)
	if idx < 0 {
		return false
	}
	remaining := strings.TrimSpace(fullText[idx+len(matched):])
	return len(remaining) <= 2 // allow 1-2 trailing chars (punctuation, etc.)
}

// findContinuationLines finds observations that appear directly below obs[startIdx]
// and likely contain a continuation of the detected value (same X band, next Y).
func findContinuationLines(observations []ocr.TextObservation, startIdx int, category string) []DetectionResult {
	src := observations[startIdx]
	srcMidX := src.X + src.Width/2
	srcBottom := src.Y + src.Height

	const (
		yGap      = 0.025 // max vertical gap between lines
		xOverlap  = 0.15  // how close the X midpoints need to be
		maxFollow = 3     // max continuation lines to follow
	)

	var results []DetectionResult
	lastBottom := srcBottom

	for count := 0; count < maxFollow; count++ {
		bestIdx := -1
		bestDist := 999.0

		for j, o := range observations {
			if j == startIdx {
				continue
			}
			// Must be below the previous line.
			if o.Y < lastBottom-0.005 {
				continue
			}
			// Must be within vertical gap.
			gap := o.Y - lastBottom
			if gap > yGap {
				continue
			}
			// Must overlap horizontally.
			oMidX := o.X + o.Width/2
			if abs(oMidX-srcMidX) > xOverlap {
				continue
			}
			// Pick the closest one below.
			if gap < bestDist {
				bestDist = gap
				bestIdx = j
			}
		}

		if bestIdx < 0 {
			break
		}

		cont := observations[bestIdx]
		// Only mask if it looks like a continuation (alphanumeric content, no common words).
		trimmed := strings.TrimSpace(cont.Text)
		if len(trimmed) < 4 || looksLikeNaturalText(trimmed) {
			break
		}

		results = append(results, DetectionResult{
			Text:       trimmed,
			Category:   category,
			X:          cont.X,
			Y:          cont.Y,
			Width:      cont.Width,
			Height:     cont.Height,
			Confidence: 0.90,
		})

		lastBottom = cont.Y + cont.Height
		startIdx = bestIdx
	}

	return results
}

// looksLikeNaturalText returns true if the text looks like normal prose
// rather than a continuation of a key/token (heuristic).
func looksLikeNaturalText(s string) bool {
	spaces := strings.Count(s, " ")
	// Tokens/keys rarely have multiple spaces; natural text usually does.
	return spaces >= 3
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// matchPatterns runs all regex patterns against text, returning detections
// with the bounding box from obs.
func matchPatterns(text string, obs ocr.TextObservation) []DetectionResult {
	var results []DetectionResult
	for _, m := range matchPatternRanges(text) {
		results = append(results, DetectionResult{
			Text:       m.text,
			Category:   m.category,
			X:          obs.X,
			Y:          obs.Y,
			Width:      obs.Width,
			Height:     obs.Height,
			Confidence: 0.95,
		})
	}
	return results
}

// patternMatch is one regex hit: the matched text, its category and its byte
// range in the trimmed input.
type patternMatch struct {
	start, end int
	text       string
	category   string
}

// matchPatternRanges runs every regex over text (trimmed) and returns the
// validated hits with their byte ranges.
func matchPatternRanges(text string) []patternMatch {
	text = strings.TrimSpace(text)
	if len(text) < 3 {
		return nil
	}

	var results []patternMatch
	for _, pat := range regexPatterns {
		matches := findRegexMatches(pat, text)
		for _, match := range matches {
			start, end := match[0], match[1]
			if pat.extract > 0 && len(match) > pat.extract*2+1 {
				start, end = match[pat.extract*2], match[pat.extract*2+1]
			}
			if start < 0 || end < start {
				continue
			}
			if pat.category == "account_number" && !accountMatchAllowed(text, match) {
				continue
			}
			matchedText := text[start:end]

			if pat.validate != nil && !pat.validate(matchedText) {
				continue
			}

			results = append(results, patternMatch{start: start, end: end, text: matchedText, category: pat.category})
		}
	}
	return results
}

type mergedLine struct {
	Text                string
	ocr.TextObservation // bounding box covering the merged line
	// members are the observations joined into Text, left to right, and
	// memberStarts their byte offsets in Text.
	members      []ocr.TextObservation
	memberStarts []int
}

// memberRegions returns one detection per observation of the line that the
// match overlaps, each with that observation's own box.
func (l mergedLine) memberRegions(m patternMatch) []DetectionResult {
	var out []DetectionResult
	for i, o := range l.members {
		start := l.memberStarts[i]
		end := start + len(strings.TrimSpace(o.Text))
		if start >= m.end || end <= m.start {
			continue
		}
		out = append(out, DetectionResult{
			Text:       m.text,
			Category:   m.category,
			X:          o.X,
			Y:          o.Y,
			Width:      o.Width,
			Height:     o.Height,
			Confidence: 0.95,
		})
	}
	return out
}

// mergeObservationsIntoLines groups OCR observations that are on the same
// horizontal line (Y within tolerance) and concatenates them left-to-right.
func mergeObservationsIntoLines(observations []ocr.TextObservation) []mergedLine {
	if len(observations) == 0 {
		return nil
	}

	const yTolerance = 0.015 // normalized; observations within this Y range are on the same line

	type group struct {
		obs []ocr.TextObservation
	}
	var groups []group

	// Simple greedy grouping: assign each observation to the first group
	// whose Y midpoint is within tolerance, or create a new group.
	for _, o := range observations {
		mid := o.Y + o.Height/2
		found := false
		for i := range groups {
			gMid := groups[i].obs[0].Y + groups[i].obs[0].Height/2
			if mid >= gMid-yTolerance && mid <= gMid+yTolerance {
				groups[i].obs = append(groups[i].obs, o)
				found = true
				break
			}
		}
		if !found {
			groups = append(groups, group{obs: []ocr.TextObservation{o}})
		}
	}

	// Only return merged lines with >1 observation (single-obs lines are
	// already covered by Pass 1).
	var lines []mergedLine
	for _, g := range groups {
		if len(g.obs) < 2 {
			continue
		}

		// Sort by X position (simple insertion sort for small N).
		sorted := make([]ocr.TextObservation, len(g.obs))
		copy(sorted, g.obs)
		for i := 1; i < len(sorted); i++ {
			key := sorted[i]
			j := i - 1
			for j >= 0 && sorted[j].X > key.X {
				sorted[j+1] = sorted[j]
				j--
			}
			sorted[j+1] = key
		}

		// Concatenate text and compute bounding box. Members whose text is
		// blank are left out so Text has no leading or doubled spaces and
		// member offsets line up with match offsets.
		var b strings.Builder
		var members []ocr.TextObservation
		var memberStarts []int
		minX, minY := sorted[0].X, sorted[0].Y
		maxX, maxY := 0.0, 0.0
		for _, o := range sorted {
			if t := strings.TrimSpace(o.Text); t != "" {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				members = append(members, o)
				memberStarts = append(memberStarts, b.Len())
				b.WriteString(t)
			}
			if o.X+o.Width > maxX {
				maxX = o.X + o.Width
			}
			if o.Y+o.Height > maxY {
				maxY = o.Y + o.Height
			}
			if o.X < minX {
				minX = o.X
			}
			if o.Y < minY {
				minY = o.Y
			}
		}

		lines = append(lines, mergedLine{
			Text: b.String(),
			TextObservation: ocr.TextObservation{
				Text:       b.String(),
				X:          minX,
				Y:          minY,
				Width:      maxX - minX,
				Height:     maxY - minY,
				Confidence: sorted[0].Confidence,
			},
			members:      members,
			memberStarts: memberStarts,
		})
	}

	return lines
}

// luhnCheck validates a credit card number using the Luhn algorithm.
func luhnCheck(s string) bool {
	// Strip spaces and dashes
	var digits []int
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits = append(digits, int(r-'0'))
		}
	}

	n := len(digits)
	if n < 13 || n > 19 {
		return false
	}

	sum := 0
	alt := false
	for i := n - 1; i >= 0; i-- {
		d := digits[i]
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}

	return sum%10 == 0
}

func looksLikePhoneNumber(s string) bool {
	digitCount := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			digitCount++
		}
	}
	return digitCount >= 10 && digitCount <= 15
}

// deduplicateDetections removes a regex detection only when an earlier
// detection of the same category already covers its entire box. Partially
// overlapping boxes are both kept: dropping one would leave the uncovered
// sliver of a sensitive match visible on the screenshot.
func deduplicateDetections(detections []DetectionResult) []DetectionResult {
	if len(detections) <= 1 {
		return detections
	}

	var result []DetectionResult
	for i, d := range detections {
		isDup := false
		for j := 0; j < i; j++ {
			if detections[j].Category == d.Category && boxContains(detections[j], d) {
				isDup = true
				break
			}
		}
		if !isDup {
			result = append(result, d)
		}
	}
	return result
}

// boxContains reports whether inner lies entirely inside outer, within a
// small epsilon for floating-point noise.
func boxContains(outer, inner DetectionResult) bool {
	const eps = 1e-9
	return inner.X >= outer.X-eps &&
		inner.Y >= outer.Y-eps &&
		inner.X+inner.Width <= outer.X+outer.Width+eps &&
		inner.Y+inner.Height <= outer.Y+outer.Height+eps
}

// boxOverlap returns the IoU (intersection over union) of two detection boxes.
func boxOverlap(a, b DetectionResult) float64 {
	x0 := max(a.X, b.X)
	y0 := max(a.Y, b.Y)
	x1 := min(a.X+a.Width, b.X+b.Width)
	y1 := min(a.Y+a.Height, b.Y+b.Height)

	if x0 >= x1 || y0 >= y1 {
		return 0
	}

	intersection := (x1 - x0) * (y1 - y0)
	areaA := a.Width * a.Height
	areaB := b.Width * b.Height
	union := areaA + areaB - intersection

	if union <= 0 {
		return 0
	}
	return intersection / union
}

// MergeDetections combines GLiNER and regex detections, removing duplicates.
// When a regex detection overlaps a GLiNER span but covers more of the OCR
// line, merge the boxes instead of dropping the regex result. Masking is
// region-based, and the broader regex box can protect OCR/GLiNER fragments
// whose exact text or bbox is noisy.
//
// Overlap is always measured against the original GLiNER boxes, never
// against a slot that an earlier regex detection already grew. Otherwise one
// merged box can chain-absorb regex boxes that never substantially overlapped
// the model box and rebuild a union that masks far more than either detector
// asked for (or pushes the region over the mask size cap and costs the whole
// frame). A merged box therefore never exceeds the union of the GLiNER box it
// came from and the regex boxes that genuinely overlap that box.
func MergeDetections(gliner, regex []DetectionResult) []DetectionResult {
	if len(regex) == 0 {
		return gliner
	}
	if len(gliner) == 0 {
		return regex
	}

	merged := make([]DetectionResult, len(gliner))
	copy(merged, gliner)
	originals := make([]DetectionResult, len(gliner))
	copy(originals, gliner)

	for _, r := range regex {
		bestIdx := -1
		bestOverlap := 0.0
		for i, g := range originals {
			overlap := boxOverlap(r, g)
			if overlap > bestOverlap {
				bestIdx = i
				bestOverlap = overlap
			}
		}
		if bestOverlap <= 0.3 {
			merged = append(merged, r)
			continue
		}
		if shouldMergeDetectionCoverage(originals[bestIdx], r) {
			merged[bestIdx] = mergeDetectionCoverage(merged[bestIdx], r)
		}
	}

	return merged
}

func shouldMergeDetectionCoverage(base, extra DetectionResult) bool {
	if addsDetectionText(base.Text, extra.Text) {
		return true
	}
	baseArea := base.Width * base.Height
	if baseArea <= 0 {
		return false
	}
	minX := min(base.X, extra.X)
	minY := min(base.Y, extra.Y)
	maxX := max(base.X+base.Width, extra.X+extra.Width)
	maxY := max(base.Y+base.Height, extra.Y+extra.Height)
	unionArea := (maxX - minX) * (maxY - minY)
	return unionArea > baseArea*1.05
}

func addsDetectionText(baseText, extraText string) bool {
	baseText = strings.TrimSpace(baseText)
	extraText = strings.TrimSpace(extraText)
	if extraText == "" {
		return false
	}
	if baseText == "" {
		return true
	}
	return !strings.Contains(strings.ToLower(baseText), strings.ToLower(extraText))
}

func mergeDetectionCoverage(base, extra DetectionResult) DetectionResult {
	minX := min(base.X, extra.X)
	minY := min(base.Y, extra.Y)
	maxX := max(base.X+base.Width, extra.X+extra.Width)
	maxY := max(base.Y+base.Height, extra.Y+extra.Height)

	merged := base
	merged.X = minX
	merged.Y = minY
	merged.Width = maxX - minX
	merged.Height = maxY - minY
	merged.Text = mergeDetectionText(base.Text, extra.Text)
	if extra.Confidence > merged.Confidence {
		merged.Confidence = extra.Confidence
	}
	return merged
}

func mergeDetectionText(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	lowerA := strings.ToLower(a)
	lowerB := strings.ToLower(b)
	if strings.Contains(lowerA, lowerB) {
		return a
	}
	if strings.Contains(lowerB, lowerA) {
		return b
	}
	return a + " " + b
}

var accountVersionValue = regexp.MustCompile(`(?i)^v?[0-9]+(?:\.[0-9]+)+`)
var accountURLToken = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9+.-]*://|www\.|(?:[a-z0-9-]+\.)+[a-z]{2,}[?:#])`)

// Account patterns capture the label in group 1 and its value in group 2.
// Latin labels must start a word; fused values must be complete numeric groups.
// Keep the existing adjoining CJK label/value behavior unchanged.
func accountMatchAllowed(text string, match []int) bool {
	labelStart, labelEnd := match[2], match[3]
	start, end := match[4], match[5]
	// Both paths (flat capture and OCR) must keep complete values. Fused
	// label/value forms use the label's left boundary, not the digit boundary.
	if end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		if accountWordRune(next) {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(text[labelStart:labelEnd])
	if !unicode.Is(unicode.Latin, first) {
		return true
	}
	if labelStart > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:labelStart])
		if unicode.IsLetter(previous) || unicode.IsNumber(previous) {
			return false
		}
	}

	// Only the label's token determines path/URL context. After a standalone
	// separated label, slashes and dots are valid parts of the identifier.
	// Optional modifiers such as " No." and " number" are captured in the
	// label itself; their whitespace/punctuation is still a separator.
	// A bare label followed by prose ("contract lapsed out") is not an
	// identifier. Explicitly delimited or numbered labels still accept alpha IDs.
	value := text[start:end]
	separator := text[labelEnd:start]
	label := strings.ToLower(text[labelStart:labelEnd])
	if strings.IndexFunc(value, unicode.IsNumber) < 0 && !strings.ContainsAny(separator, ":：=#") && !strings.Contains(label, "number") && !strings.Contains(label, "no") && !strings.HasSuffix(label, "id") {
		return false
	}
	fused := labelEnd == start && strings.IndexFunc(text[labelStart:labelEnd], func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) < 0
	left := labelStart
	for left > 0 {
		previous, size := utf8.DecodeLastRuneInString(text[:left])
		if unicode.IsSpace(previous) {
			break
		}
		left -= size
	}
	tokenEnd := labelEnd
	if fused {
		right := strings.IndexFunc(text[labelEnd:], unicode.IsSpace)
		if right < 0 {
			right = len(text) - labelEnd
		}
		tokenEnd += right
	}
	token := strings.TrimLeft(text[left:tokenEnd], "\"'([{<")
	if strings.ContainsAny(token, `/\`) || accountURLToken.MatchString(token) {
		return false
	}
	if accountVersionValue.MatchString(text[start:]) && (fused || text[start] == 'v' || text[start] == 'V') {
		return false
	}
	if fused {
		value := text[start:end]
		if !accountNumericValuePresent(value) {
			return false
		}
		if end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || next == '_' {
				return false
			}
		}
	}
	return true
}

func accountNumericValuePresent(value string) bool {
	digits := 0
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits++
			continue
		}
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return digits >= 3
}

// Do not backtrack from a complete label into its own "number"/"id" suffix
// when a stored mask (or no value) follows it.
func accountValuePresent(value string) bool {
	if utf8.RuneCountInString(value) < 3 {
		return false
	}
	// A label ending in a colon is not a value. In multiline insurance text,
	// a short generic label must not consume the next field's label.
	trimmed := strings.TrimSpace(value)
	first := trimmed
	if end := strings.IndexFunc(first, unicode.IsSpace); end >= 0 {
		first = first[:end]
	}
	if strings.IndexFunc(first, unicode.IsNumber) < 0 && (strings.HasSuffix(first, ":") || strings.HasSuffix(first, "：")) {
		return false
	}
	switch strings.ToLower(strings.Trim(value, " \t\r\n:：=#")) {
	case "number", "no", "no.", "id":
		return false
	default:
		return true
	}
}

func accountWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_'
}

func accountTokenOffsetsAllowed(text string, start, end int) bool {
	return start >= 0 && start < end && end <= len(text) && utf8.RuneStart(text[start]) &&
		(end == len(text) || utf8.RuneStart(text[end])) && utf8.ValidString(text[start:end])
}

func accountBoundaryRune(r rune, numeric bool) bool {
	// Chinese and Japanese prose commonly touches a complete numeric value.
	// Keep letters, marks and digits joined for Latin/alphanumeric identifiers.
	if numeric && (unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)) {
		return false
	}
	return accountWordRune(r)
}

// OPF spans can align to subword tokens. Reject account spans cutting a word;
// whitespace belongs outside the value and must not hide that boundary.
func accountSpanAllowed(text string, start, end int) bool {
	if !accountTokenOffsetsAllowed(text, start, end) {
		return false
	}
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	for start < end {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if start >= end {
		return false
	}
	numeric := strings.IndexFunc(text[start:end], func(r rune) bool { return !unicode.IsDigit(r) }) < 0
	if start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if r == utf8.RuneError && size == 1 || accountBoundaryRune(r, numeric) {
			return false
		}
	}
	if end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if r == utf8.RuneError && size == 1 || accountBoundaryRune(r, numeric) {
			return false
		}
	}
	return true
}

// A rejected generic label may have consumed a subsequent field's label
// ("保険\n証券番号:"). Resume after the rejected label, so that field can
// still protect its real value. Both flat capture and OCR use this iterator.
func findRegexMatches(pat regexPattern, text string) [][]int {
	if pat.category != "account_number" {
		return pat.re.FindAllStringSubmatchIndex(text, -1)
	}
	var matches [][]int
	for offset := 0; offset < len(text); {
		match := pat.re.FindStringSubmatchIndex(text[offset:])
		if match == nil {
			break
		}
		for i := range match {
			if match[i] >= 0 {
				match[i] += offset
			}
		}
		if !accountMatchAllowed(text, match) || pat.validate != nil && !pat.validate(text[match[4]:match[5]]) {
			offset = match[3]
			continue
		}
		matches = append(matches, match)
		offset = match[1]
	}
	return matches
}
