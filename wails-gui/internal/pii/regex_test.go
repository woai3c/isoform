package pii

import (
	"image/color"
	"strings"
	"testing"

	"wails-gui/internal/platform/ocr"
)

func TestContinuationDetection(t *testing.T) {
	// Simulate an API key that wraps across two OCR observations.
	observations := []ocr.TextObservation{
		{Text: "sk-or-v1-38567695f6b955c3a6e40bdb2b", X: 0.10, Y: 0.50, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "99019abcb0116909f8e6c11150d7b6202a575", X: 0.10, Y: 0.52, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "some unrelated text over here", X: 0.60, Y: 0.50, Width: 0.20, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)

	// Should detect at least 2 regions: the sk- line + the continuation line.
	apiKeyCount := 0
	for _, r := range results {
		if r.Category == "api_key" {
			apiKeyCount++
			t.Logf("detected: category=%s text=%q (%.2f,%.2f)", r.Category, r.Text, r.X, r.Y)
		}
	}

	if apiKeyCount < 2 {
		t.Errorf("expected at least 2 api_key detections (original + continuation), got %d", apiKeyCount)
	}
}

func TestContinuationDoesNotFollowNaturalText(t *testing.T) {
	// The continuation should stop if the next line looks like natural text.
	observations := []ocr.TextObservation{
		{Text: "sk-or-v1-38567695f6b955c3a6e40bdb2b", X: 0.10, Y: 0.50, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "this is a normal sentence with spaces", X: 0.10, Y: 0.52, Width: 0.30, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)

	apiKeyCount := 0
	for _, r := range results {
		if r.Category == "api_key" {
			apiKeyCount++
		}
	}

	if apiKeyCount != 1 {
		t.Errorf("expected exactly 1 api_key detection (no continuation into natural text), got %d", apiKeyCount)
	}
}

func TestCreditCardGroupFormat(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "4532 0123 4567", X: 0.10, Y: 0.50, Width: 0.20, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)

	found := false
	for _, r := range results {
		if r.Category == "credit_card" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected credit_card detection for 3-group-of-4 format")
	}
}

func TestContactAndPolicyRegexes(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "email: person@example.com", X: 0.10, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 1.0},
		{Text: "电话: +86 000 0013 8000", X: 0.10, Y: 0.20, Width: 0.20, Height: 0.02, Confidence: 1.0},
		{Text: "保单号为 AB-1234-XYZ", X: 0.10, Y: 0.30, Width: 0.20, Height: 0.02, Confidence: 1.0},
		{Text: "車両番号は 品川 300 あ 12-34", X: 0.10, Y: 0.40, Width: 0.20, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	categories := map[string]int{}
	for _, r := range results {
		categories[r.Category]++
	}

	if categories["private_email"] == 0 {
		t.Fatal("expected private_email detection")
	}
	if categories["private_phone"] == 0 {
		t.Fatal("expected private_phone detection")
	}
	if categories["account_number"] == 0 {
		t.Fatal("expected account_number detection for policy identifier")
	}
	if categories["license_plate"] == 0 {
		t.Fatal("expected license_plate detection for vehicle identifier")
	}
}

func TestPhoneRegexHandlesNaturalContext(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "Please call me at +1 917 555 0188 before refunding the card.", X: 0.10, Y: 0.10, Width: 0.40, Height: 0.02, Confidence: 1.0},
		{Text: "Support phone on file: +1 (305) 555-0109", X: 0.10, Y: 0.20, Width: 0.40, Height: 0.02, Confidence: 1.0},
		{Text: "Reach customer via 03-1234-5678 after 5pm", X: 0.10, Y: 0.30, Width: 0.40, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	if got := countDetections(results, "private_phone"); got < 3 {
		t.Fatalf("expected natural-context phone detections, got %d (%+v)", got, results)
	}
}

func TestPhoneRegexAvoidsShortOperationalNumbers(t *testing.T) {
	tests := []string{
		"Call scheduled for 2026-06-04 after deploy.",
		"Support phone on file: ticket 123456789 is still open.",
		"Reach customer after build 20260604 completed in 42.0s.",
	}
	for _, text := range tests {
		results := DetectWithRegex([]ocr.TextObservation{
			{Text: text, X: 0.10, Y: 0.10, Width: 0.40, Height: 0.02, Confidence: 1.0},
		})
		if countDetections(results, "private_phone") != 0 {
			t.Fatalf("expected no phone detection for %q, got %+v", text, results)
		}
	}
}

func TestJapaneseInsuranceVehicleAndIdentityRegexes(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "保険会社: 東京海上日動火災保険", X: 0.10, Y: 0.10, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "証券番号 JP-88-1234567", X: 0.10, Y: 0.20, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "登録番号 品川 300 あ 12-34", X: 0.10, Y: 0.30, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "マイナンバー: 1234-5678-9012", X: 0.10, Y: 0.40, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "〒100-0001", X: 0.10, Y: 0.50, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "電話番号: 03ー1234ー5678", X: 0.10, Y: 0.60, Width: 0.30, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	categories := map[string]int{}
	for _, r := range results {
		categories[r.Category]++
	}

	for _, category := range []string{
		"private_org",
		"account_number",
		"license_plate",
		"government_id",
		"private_address",
		"private_phone",
	} {
		if categories[category] == 0 {
			t.Fatalf("expected %s detection, got categories=%v", category, categories)
		}
	}
}

func TestMultilingualInsuranceVehicleAndIdentityRegexes(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "Insurer: State Farm Policy No: SF-123-456 License Plate: 7ABC123", X: 0.10, Y: 0.10, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "保险公司：平安产险 保单号 PA-98765 车牌号 粤B12345", X: 0.10, Y: 0.20, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "보험회사: 삼성화재 증권번호 KR-55-7788 차량번호 12가 3456 주민등록번호 900101-1234567", X: 0.10, Y: 0.30, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "Assureur: AXA numéro de police FR-2024-778 plaque d'immatriculation AB-123-CD", X: 0.10, Y: 0.40, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "Versicherer: Allianz Versicherungsnummer DE-7788 Kennzeichen B AB 1234", X: 0.10, Y: 0.50, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "Aseguradora: Mapfre número de póliza ES-5566 matrícula 1234 ABC", X: 0.10, Y: 0.60, Width: 0.30, Height: 0.02, Confidence: 1.0},
		{Text: "CPF: 123.456.789-09 IMEI: 356938035643809", X: 0.10, Y: 0.70, Width: 0.30, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	categories := map[string]int{}
	for _, r := range results {
		categories[r.Category]++
	}

	if categories["private_org"] < 5 {
		t.Fatalf("expected multilingual insurance org detections, got %d (%v)", categories["private_org"], categories)
	}
	if categories["account_number"] < 5 {
		t.Fatalf("expected multilingual policy/account detections, got %d (%v)", categories["account_number"], categories)
	}
	if categories["license_plate"] < 5 {
		t.Fatalf("expected multilingual license plate detections, got %d (%v)", categories["license_plate"], categories)
	}
	if categories["government_id"] < 2 {
		t.Fatalf("expected government id detections, got %d (%v)", categories["government_id"], categories)
	}
}

func TestMultilingualRegexesAvoidShortLabelSubstrings(t *testing.T) {
	redacted, count := RedactText("Japan planning notes mention panel updates and invoices but no identifiers.")
	if count != 0 {
		t.Fatalf("expected no redaction for short-label substrings, got count=%d text=%q", count, redacted)
	}
}

func TestDeviceAndSessionIdentifierRegexes(t *testing.T) {
	redacted, count := RedactText(`session_id=abc123def456ghi789 IMEI: 356938035643809 advertising_id=38400000-8cf0-11bd-b23e-10b96e40000d`)
	if count < 3 {
		t.Fatalf("RedactText count = %d, want at least 3; text=%q", count, redacted)
	}
	for _, raw := range []string{
		"abc123def456ghi789",
		"356938035643809",
		"38400000-8cf0-11bd-b23e-10b96e40000d",
	} {
		if strings.Contains(redacted, raw) {
			t.Fatalf("redacted text still contains %q: %q", raw, redacted)
		}
	}
}

func TestOCRTolerantCredentialRegexes(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "Password: Admin Portal # 2026 !", X: 0.10, Y: 0.10, Width: 0.25, Height: 0.02, Confidence: 1.0},
		{Text: "2FA code: 831 204", X: 0.10, Y: 0.20, Width: 0.20, Height: 0.02, Confidence: 1.0},
		{Text: "SESSION_SECRET=sess-fixture-example", X: 0.10, Y: 0.30, Width: 0.35, Height: 0.02, Confidence: 1.0},
		{Text: "reset_tokenert_live_8d6f2c9a7b4e1d0c3a2b", X: 0.10, Y: 0.40, Width: 0.35, Height: 0.02, Confidence: 1.0},
		{Text: "DATABASE_URL=postgres://app:P@ssw0rd ! DB@db:5432/app", X: 0.10, Y: 0.50, Width: 0.45, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	if countDetections(results, "password") < 2 {
		t.Fatalf("expected password detection, got %+v", results)
	}
	if countDetections(results, "one_time_password") < 1 {
		t.Fatalf("expected one_time_password detection, got %+v", results)
	}
	if countDetections(results, "session_id") < 2 {
		t.Fatalf("expected session_id detections for delimiter and fused OCR forms, got %+v", results)
	}
}

func TestOCRFragmentedLineRegexes(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "Email:", X: 0.10, Y: 0.10, Width: 0.05, Height: 0.02, Confidence: 1.0},
		{Text: "alex . ops @", X: 0.16, Y: 0.10, Width: 0.10, Height: 0.02, Confidence: 1.0},
		{Text: "example . com", X: 0.27, Y: 0.10, Width: 0.11, Height: 0.02, Confidence: 1.0},
		{Text: "Phone:", X: 0.10, Y: 0.20, Width: 0.05, Height: 0.02, Confidence: 1.0},
		{Text: "+1 (", X: 0.16, Y: 0.20, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "415 )", X: 0.21, Y: 0.20, Width: 0.05, Height: 0.02, Confidence: 1.0},
		{Text: "555 - 0198", X: 0.27, Y: 0.20, Width: 0.10, Height: 0.02, Confidence: 1.0},
		{Text: "Card", X: 0.10, Y: 0.30, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.16, Y: 0.30, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.21, Y: 0.30, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.26, Y: 0.30, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.31, Y: 0.30, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "2FA", X: 0.10, Y: 0.40, Width: 0.03, Height: 0.02, Confidence: 1.0},
		{Text: "code:", X: 0.14, Y: 0.40, Width: 0.05, Height: 0.02, Confidence: 1.0},
		{Text: "831", X: 0.20, Y: 0.40, Width: 0.03, Height: 0.02, Confidence: 1.0},
		{Text: "204", X: 0.24, Y: 0.40, Width: 0.03, Height: 0.02, Confidence: 1.0},
		{Text: "session_id", X: 0.10, Y: 0.50, Width: 0.08, Height: 0.02, Confidence: 1.0},
		{Text: "=", X: 0.19, Y: 0.50, Width: 0.01, Height: 0.02, Confidence: 1.0},
		{Text: "prod_sess_7f6e5d4c3b2a1098", X: 0.21, Y: 0.50, Width: 0.22, Height: 0.02, Confidence: 1.0},
		{Text: "Account", X: 0.10, Y: 0.60, Width: 0.07, Height: 0.02, Confidence: 1.0},
		{Text: "No:", X: 0.18, Y: 0.60, Width: 0.03, Height: 0.02, Confidence: 1.0},
		{Text: "CUST", X: 0.22, Y: 0.60, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "-", X: 0.27, Y: 0.60, Width: 0.01, Height: 0.02, Confidence: 1.0},
		{Text: "884211", X: 0.29, Y: 0.60, Width: 0.06, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	for _, category := range []string{
		"private_email",
		"private_phone",
		"credit_card",
		"one_time_password",
		"session_id",
		"account_number",
	} {
		if countDetections(results, category) == 0 {
			t.Fatalf("expected OCR-fragmented %s detection, got %+v", category, results)
		}
	}
}

func TestOCRTolerantCredentialRegexesAvoidPasswordPolicyText(t *testing.T) {
	redacted, count := RedactText("Passwords should be rotated monthly and stored in the approved vault.")
	if count != 0 {
		t.Fatalf("expected no redaction for password policy prose, got count=%d text=%q", count, redacted)
	}
}

func TestPasswordRegexDoesNotConsumeFollowingSentence(t *testing.T) {
	redacted, count := RedactText("Password: admin123. Please do not share this with support.")
	if count == 0 {
		t.Fatalf("expected password redaction, got text=%q", redacted)
	}
	if !strings.Contains(redacted, "Please do not share this with support.") {
		t.Fatalf("password regex consumed following sentence: %q", redacted)
	}
}

func TestDSNPasswordRegexAvoidsURLPathQueryEmail(t *testing.T) {
	results := DetectWithRegex([]ocr.TextObservation{
		{
			Text:       "Open http://host:8080/some_file.php?email=user@example.com for the report",
			X:          0.10,
			Y:          0.10,
			Width:      0.40,
			Height:     0.02,
			Confidence: 1.0,
		},
	})
	if countDetections(results, "password") != 0 {
		t.Fatalf("expected no password detection for URL path/query email, got %+v", results)
	}
}

func TestMergeDetectionsExpandsOverlappingRegexCoverage(t *testing.T) {
	gliner := []DetectionResult{
		{
			Text:       "P rodDb ! 2026",
			Category:   "password",
			X:          0.18,
			Y:          0.15,
			Width:      0.16,
			Height:     0.03,
			Confidence: 0.80,
		},
	}
	regex := []DetectionResult{
		{
			Text:       "DB_PASSWORD=P rodDb ! 2026 #ChangeMe",
			Category:   "password",
			X:          0.03,
			Y:          0.15,
			Width:      0.32,
			Height:     0.03,
			Confidence: 0.95,
		},
	}

	merged := MergeDetections(gliner, regex)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
	if merged[0].X > regex[0].X+1e-9 || merged[0].Width < regex[0].Width-1e-9 {
		t.Fatalf("merged bbox did not expand to regex coverage: %+v", merged[0])
	}
	if !strings.Contains(merged[0].Text, "ChangeMe") {
		t.Fatalf("merged text did not preserve regex text: %+v", merged[0])
	}
}

func TestMergeDetectionsDropsNearDuplicateRegex(t *testing.T) {
	gliner := []DetectionResult{
		{Text: "secret123", Category: "api_key", X: 0.10, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.80},
	}
	regex := []DetectionResult{
		{Text: "secret123", Category: "api_key", X: 0.10, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.95},
	}

	merged := MergeDetections(gliner, regex)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
}

func TestMergeDetectionsPreservesSameBoxDistinctRegexText(t *testing.T) {
	gliner := []DetectionResult{
		{Text: "5432", Category: "cvv", X: 0.03, Y: 0.09, Width: 0.52, Height: 0.03, Confidence: 0.50},
	}
	regex := []DetectionResult{
		{Text: "P@ssw0rd!DB", Category: "password", X: 0.03, Y: 0.09, Width: 0.52, Height: 0.03, Confidence: 0.95},
	}

	merged := MergeDetections(gliner, regex)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
	if !strings.Contains(merged[0].Text, "P@ssw0rd!DB") {
		t.Fatalf("merged text did not preserve same-box password text: %+v", merged[0])
	}
}

// Two same-category boxes that mostly overlap are both kept: dropping the
// second would leave the sliver only it covers (the tail of a sensitive
// match) visible on the screenshot.
func TestDeduplicateDetectionsKeepsPartialOverlap(t *testing.T) {
	detections := []DetectionResult{
		{Text: "sk-live-aaaa", Category: "api_key", X: 0.10, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.95},
		{Text: "sk-live-aaaa", Category: "api_key", X: 0.14, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.95},
	}

	got := deduplicateDetections(detections)
	if len(got) != 2 {
		t.Fatalf("len(deduplicateDetections) = %d, want 2 (partial overlap keeps both boxes): %+v", len(got), got)
	}
	sliverCovered := false
	for _, d := range got {
		if d.X <= 0.30+1e-9 && d.X+d.Width >= 0.34-1e-9 {
			sliverCovered = true
			break
		}
	}
	if !sliverCovered {
		t.Fatalf("sliver [0.30,0.34] of the second box is unmasked after dedup: %+v", got)
	}
}

// A box fully contained in an earlier same-category box is a duplicate and
// is removed; containment inside another category's box does not count.
func TestDeduplicateDetectionsDropsFullyContained(t *testing.T) {
	detections := []DetectionResult{
		{Text: "person@example.com", Category: "private_email", X: 0.10, Y: 0.10, Width: 0.30, Height: 0.02, Confidence: 0.95},
		{Text: "person", Category: "private_email", X: 0.15, Y: 0.105, Width: 0.05, Height: 0.01, Confidence: 0.95},
		{Text: "person", Category: "account_number", X: 0.15, Y: 0.105, Width: 0.05, Height: 0.01, Confidence: 0.95},
	}

	got := deduplicateDetections(detections)
	if len(got) != 2 {
		t.Fatalf("len(deduplicateDetections) = %d, want 2 (contained same-category box dropped, other category kept): %+v", len(got), got)
	}
	if got[0].Category != "private_email" || got[1].Category != "account_number" {
		t.Fatalf("unexpected survivors after dedup: %+v", got)
	}
}

// A regex box that lands inside a larger GLiNER box merges without growing
// the GLiNER box: the union of the two inputs is the GLiNER box itself.
func TestMergeDetectionsRegexInsideGlinerDoesNotGrow(t *testing.T) {
	gliner := []DetectionResult{
		{Text: "the api key is sk-live", Category: "api_key", X: 0.10, Y: 0.10, Width: 0.40, Height: 0.02, Confidence: 0.80},
	}
	regex := []DetectionResult{
		{Text: "sk-live-9f8c", Category: "api_key", X: 0.15, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.95},
	}

	merged := MergeDetections(gliner, regex)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
	g := gliner[0]
	if m := merged[0]; abs(m.X-g.X) > 1e-9 || abs(m.Y-g.Y) > 1e-9 || abs(m.Width-g.Width) > 1e-9 || abs(m.Height-g.Height) > 1e-9 {
		t.Fatalf("merged box %+v grew beyond the containing GLiNER box %+v", m, g)
	}
}

// Merge growth must not chain: a regex box whose only substantial overlap is
// with an already-grown union (not with the original GLiNER box) keeps its
// own box instead of being absorbed into an oversized union.
func TestMergeDetectionsDoesNotChainGrowAcrossLine(t *testing.T) {
	gliner := []DetectionResult{
		{Text: "otp one", Category: "one_time_password", X: 0.10, Y: 0.10, Width: 0.20, Height: 0.02, Confidence: 0.80},
	}
	regex := []DetectionResult{
		{Text: "otp two", Category: "one_time_password", X: 0.18, Y: 0.10, Width: 0.16, Height: 0.02, Confidence: 0.95},
		{Text: "otp three", Category: "one_time_password", X: 0.24, Y: 0.10, Width: 0.16, Height: 0.02, Confidence: 0.95},
	}

	merged := MergeDetections(gliner, regex)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2 (no chained growth): %+v", len(merged), merged)
	}
	if m := merged[0]; m.X < 0.10-1e-9 || m.X > 0.10+1e-9 || m.X+m.Width < 0.34-1e-9 || m.X+m.Width > 0.34+1e-9 {
		t.Fatalf("merged[0] = %+v, want the [0.10,0.34] union of the GLiNER box and the first regex box", m)
	}
	if m := merged[1]; m.X != 0.24 || m.Width != 0.16 {
		t.Fatalf("merged[1] = %+v, want the second regex box kept as-is", m)
	}
	for _, m := range merged {
		if m.X+m.Width > 0.40+1e-9 {
			t.Fatalf("box %+v exceeds the union of the boxes it came from", m)
		}
	}
}

// A regex match inside a merged OCR row is masked with the boxes of the
// member observations it covers. With CJK text each rune takes three UTF-8
// bytes; member byte offsets must still line up so the value member keeps
// its own box and the other members stay unmasked.
func TestPass2MemberBoxesWithCJKText(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "保单号", X: 0.10, Y: 0.10, Width: 0.06, Height: 0.02, Confidence: 1.0},
		{Text: "为", X: 0.17, Y: 0.10, Width: 0.02, Height: 0.02, Confidence: 1.0},
		{Text: "AB-1234-XYZ", X: 0.20, Y: 0.10, Width: 0.16, Height: 0.02, Confidence: 1.0},
		{Text: "（内部）", X: 0.38, Y: 0.10, Width: 0.08, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1 account_number detection: %+v", len(results), results)
	}
	r := results[0]
	if r.Category != "account_number" {
		t.Fatalf("category = %q, want account_number: %+v", r.Category, r)
	}
	value := observations[2]
	if r.X != value.X || r.Y != value.Y || r.Width != value.Width || r.Height != value.Height {
		t.Fatalf("box %+v does not match the matched member's own box %+v", r, value)
	}
}

// Multi-byte emoji (one pictograph, one two-codepoint flag) in a leading
// member must not shift member byte offsets: the mask box is the value
// member's own box, not a byte-proportional slice of the row.
func TestPass2MemberBoxesWithEmojiText(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "\U0001F510\U0001F1E8\U0001F1F3", X: 0.05, Y: 0.10, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "session_id", X: 0.10, Y: 0.10, Width: 0.08, Height: 0.02, Confidence: 1.0},
		{Text: "=", X: 0.19, Y: 0.10, Width: 0.01, Height: 0.02, Confidence: 1.0},
		{Text: "prod_sess_7f6e5d4c3b2a1098", X: 0.21, Y: 0.10, Width: 0.22, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1 session_id detection: %+v", len(results), results)
	}
	r := results[0]
	if r.Category != "session_id" {
		t.Fatalf("category = %q, want session_id: %+v", r.Category, r)
	}
	value := observations[3]
	if r.X != value.X || r.Y != value.Y || r.Width != value.Width || r.Height != value.Height {
		t.Fatalf("box %+v does not match the matched member's own box %+v", r, value)
	}
}

func TestPrivateLinkRegexes(t *testing.T) {
	tests := []string{
		"https://bucket.s3.amazonaws.com/statement.pdf?X-Amz-Signature=abcdef1234567890",
		"https://storage.googleapis.com/bucket/statement.pdf?X-Goog-Signature=abcdef1234567890",
		"https://acct.blob.core.windows.net/container/statement.pdf?sv=2024-11-04&se=2026-06-02&sp=r&sig=abcdef1234567890",
		"https://drive.google.com/file/d/1Abcdefghijklmnopqrstuvwxyz1234567890/view",
	}

	for _, raw := range tests {
		results := DetectWithRegex([]ocr.TextObservation{
			{Text: raw, X: 0.10, Y: 0.10, Width: 0.40, Height: 0.02, Confidence: 1.0},
		})
		if countDetections(results, "private_link") == 0 {
			t.Fatalf("expected private_link detection for %q", raw)
		}
	}
}

func TestPrivateLinkRegexAvoidsGenericShortQueryParams(t *testing.T) {
	raw := "https://example.com/view?sp=spreadsheet&sv=preview&se=section12345"
	results := DetectWithRegex([]ocr.TextObservation{
		{Text: raw, X: 0.10, Y: 0.10, Width: 0.40, Height: 0.02, Confidence: 1.0},
	})
	if countDetections(results, "private_link") != 0 {
		t.Fatalf("expected no private_link detection for generic query params, got %+v", results)
	}
}

func TestRedactTextUsesExpandedPIIPatterns(t *testing.T) {
	redacted, count := RedactText("email person@example.com, 电话: +86 000 0013 8000, 保单号为 AB-1234-XYZ")
	if count < 3 {
		t.Fatalf("RedactText count = %d, want at least 3; text=%q", count, redacted)
	}
	for _, raw := range []string{"person@example.com", "+86 000 0013 8000", "AB-1234-XYZ"} {
		if strings.Contains(redacted, raw) {
			t.Fatalf("redacted text still contains %q: %q", raw, redacted)
		}
	}
}

func TestRedactTextRecognizesFilenameSecrets(t *testing.T) {
	for _, test := range []struct {
		name  string
		want  string
		count int
	}{
		{"verification code 482913.txt", "verification code [REDACTED:one_time_password].txt", 1},
		{"verification_code_482913.txt", "verification_code_[REDACTED:one_time_password].txt", 1},
		{"verification-code-482913.txt", "verification-code-[REDACTED:one_time_password].txt", 1},
		{"verification__code__482_913_archive.txt", "verification__code__[REDACTED:one_time_password]_archive.txt", 1},
		{"OTP_482913.txt", "OTP_[REDACTED:one_time_password].txt", 1},
		{"notes_ghp_1234567890abcdefghijklmnopqrstuvwxyz.txt", "notes_[REDACTED:api_key].txt", 1},
		{"notes_sk-proj-1234567890abcdefghijklmnop.txt", "notes_[REDACTED:api_key].txt", 1},
		{"2410.14224v1.pdf", "2410.14224v1.pdf", 0},
		{"Report 2026-10-17.pdf", "Report 2026-10-17.pdf", 0},
		{"verification_report_2026.txt", "verification_report_2026.txt", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, count := RedactText(test.name)
			if got != test.want || count != test.count {
				t.Fatalf("RedactText(%q) = (%q, %d), want (%q, %d)", test.name, got, count, test.want, test.count)
			}
		})
	}
}

func countDetections(results []DetectionResult, category string) int {
	count := 0
	for _, r := range results {
		if r.Category == category {
			count++
		}
	}
	return count
}

func TestLuhnCheck(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"4532015112830366", true},  // valid Visa
		{"5440150182137679", true},  // valid MC
		{"1234567890123456", false}, // invalid
		{"4532 0151 1283 0366", true},
	}

	for _, tt := range tests {
		if got := luhnCheck(tt.input); got != tt.valid {
			t.Errorf("luhnCheck(%q) = %v, want %v", tt.input, got, tt.valid)
		}
	}
}

// A card number split across OCR pieces that share a row with a tall heading
// is masked piece by piece. The row's union box would cover 30% of the frame,
// over the mask cap, and the whole frame would be dropped.
func TestPass2MasksMatchedMembersNotTheRow(t *testing.T) {
	observations := []ocr.TextObservation{
		{Text: "Billing", X: 0.05, Y: 0.05, Width: 0.50, Height: 0.60, Confidence: 1.0}, // tall heading, row midpoint 0.35
		{Text: "4242", X: 0.60, Y: 0.34, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.65, Y: 0.34, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.70, Y: 0.34, Width: 0.04, Height: 0.02, Confidence: 1.0},
		{Text: "4242", X: 0.75, Y: 0.34, Width: 0.04, Height: 0.02, Confidence: 1.0},
	}

	results := DetectWithRegex(observations)
	if countDetections(results, "credit_card") == 0 {
		t.Fatalf("card number split across the row not detected: %+v", results)
	}
	for _, r := range results {
		if r.Width*r.Height > 0.05 {
			t.Fatalf("detection %+v spans the tall heading; want only the card pieces", r)
		}
	}
	covered := 0
	for _, o := range observations[1:] {
		for _, r := range results {
			if r.Category == "credit_card" && r.X <= o.X && r.X+r.Width >= o.X+o.Width {
				covered++
				break
			}
		}
	}
	if covered != 4 {
		t.Fatalf("%d of 4 card pieces masked, want all: %+v", covered, results)
	}
	if _, err := MaskRegions(makeSolidJPEG(t, 200, 200, color.RGBA{A: 255}), results, 85); err != nil {
		t.Fatalf("MaskRegions = %v, want the frame masked, not dropped", err)
	}
}
