package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"wails-gui/internal/pii/eval"
)

const (
	imgWidth           = 1920
	imgHeight          = 1080
	defaultJPEGQuality = 50
)

func main() {
	outDir := flag.String("out", "./internal/pii/eval/testdata/screenshots", "output directory for generated screenshots")
	quality := flag.Int("quality", defaultJPEGQuality, "JPEG quality for generated screenshots")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	fontData, err := os.ReadFile("/System/Library/Fonts/Supplemental/Andale Mono.ttf")
	if err != nil {
		fontData, err = os.ReadFile("/System/Library/Fonts/Supplemental/Courier New.ttf")
		if err != nil {
			fontData, err = os.ReadFile("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf")
			if err != nil {
				log.Fatal("no monospace font found")
			}
		}
	}

	ft, err := opentype.Parse(fontData)
	if err != nil {
		log.Fatalf("parse font: %v", err)
	}

	scenarios := buildScenarios()

	// Validate and auto-fix all scenarios before generating
	for si := range scenarios {
		for li := range scenarios[si].Lines {
			line := &scenarios[si].Lines[li]
			for pi := range line.PIISpans {
				span := &line.PIISpans[pi]
				if span.Text != "" {
					idx := strings.Index(line.Text, span.Text)
					if idx < 0 {
						log.Fatalf("scenario %q line %d span %d: PIIText %q not found in text=%q",
							scenarios[si].ID, li, pi, span.Text, line.Text)
					}
					span.Start = idx
					span.End = idx + len(span.Text)
				}
				if span.End > len(line.Text) {
					span.End = len(line.Text)
				}
				if span.Category == "" || span.Start >= span.End {
					log.Fatalf("scenario %q line %d span %d: invalid span [%d:%d] category=%q text=%q",
						scenarios[si].ID, li, pi, span.Start, span.End, span.Category, line.Text)
				}
			}
			if line.IsPII {
				if line.PIIText != "" {
					idx := strings.Index(line.Text, line.PIIText)
					if idx < 0 {
						log.Fatalf("scenario %q line %d: PIIText %q not found in text=%q",
							scenarios[si].ID, li, line.PIIText, line.Text)
					}
					line.PIIStart = idx
					line.PIIEnd = idx + len(line.PIIText)
				}
				if line.PIIEnd > len(line.Text) {
					line.PIIEnd = len(line.Text)
				}
				if line.PIIStart >= line.PIIEnd {
					log.Fatalf("scenario %q line %d: invalid span [%d:%d] text=%q",
						scenarios[si].ID, li, line.PIIStart, line.PIIEnd, line.Text)
				}
			}
		}
	}

	var samples []eval.Sample
	for _, sc := range scenarios {
		imgPath := filepath.Join(*outDir, sc.ID+".jpg")
		annotations := renderScenario(sc, ft, imgPath, *quality)

		samples = append(samples, eval.Sample{
			ID:          sc.ID,
			ImagePath:   imgPath,
			Language:    sc.Language,
			Annotations: annotations,
		})
		fmt.Printf("Generated: %s (%d annotations)\n", imgPath, len(annotations))
	}

	ds := eval.Dataset{
		Name:        "yansu-pii-eval-screenshots-v1",
		Description: "Screenshot-based evaluation dataset with pixel-accurate PII annotations for GLiNER + regex evaluation",
		Version:     "1.1.0",
		Samples:     samples,
	}

	dsPath := filepath.Join(*outDir, "dataset.json")
	if err := eval.SaveDataset(dsPath, &ds); err != nil {
		log.Fatalf("save dataset: %v", err)
	}
	fmt.Printf("\nDataset saved: %s (%d samples)\n", dsPath, len(samples))
}

type piiSpan struct {
	Category string
	Text     string
	Start    int
	End      int
}

type textLine struct {
	Text     string
	IsPII    bool
	Category string
	PIIText  string
	PIIStart int // char offset within line where PII starts
	PIIEnd   int // char offset within line where PII ends
	PIISpans []piiSpan
}

type scenario struct {
	ID       string
	Title    string
	Language string
	BgColor  color.Color
	FgColor  color.Color
	FontSize float64
	Lines    []textLine
}

func buildScenarios() []scenario {
	darkBg := color.RGBA{R: 30, G: 30, B: 30, A: 255}
	lightBg := color.RGBA{R: 248, G: 248, B: 248, A: 255}
	whiteBg := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	darkFg := color.RGBA{R: 33, G: 33, B: 33, A: 255}
	lightFg := color.RGBA{R: 220, G: 220, B: 220, A: 255}

	return []scenario{
		{
			ID: "terminal-api-keys", Title: "Terminal with API Keys", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 14,
			Lines: []textLine{
				{Text: "$ export OPENAI_API_KEY=sk-test-example-api-key"},
				{Text: "sk-test-example-api-key", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 23},
				{Text: "$ export GITHUB_TOKEN=ghp_aBcDeFgHiJkLmNoPqRsTuVw"},
				{Text: "ghp_aBcDeFgHiJkLmNoPqRsTuVw", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 28},
				{Text: "$ export AWS_KEY=AKIAIOSFODNN7EXAMPLE"},
				{Text: "AKIAIOSFODNN7EXAMPLE", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 20},
				{Text: "$ export SLACK=xoxb-12345678-abcdefgh"},
				{Text: "xoxb-12345678-abcdefgh", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 22},
				{Text: "$ echo 'done'"},
				{Text: "done"},
			},
		},
		{
			ID: "form-personal-info", Title: "Personal Info Form", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 15,
			Lines: []textLine{
				{Text: "--- Personal Information ---"},
				{Text: ""},
				{Text: "Full Name:      John Michael Smith"},
				{Text: "Email:          john.smith@example.com", IsPII: true, Category: "private_email", PIIStart: 16, PIIEnd: 38},
				{Text: "Phone:          +1 (415) 555-0198", IsPII: true, Category: "private_phone", PIIStart: 16, PIIEnd: 34},
				{Text: "SSN:            078-05-1120", IsPII: true, Category: "government_id", PIIStart: 16, PIIEnd: 27},
				{Text: ""},
				{Text: "--- Payment Information ---"},
				{Text: ""},
				{Text: "Card Number:    4532 0123 4567 8901", IsPII: true, Category: "credit_card", PIIStart: 16, PIIEnd: 35},
				{Text: "CVV:            847", IsPII: true, Category: "cvv", PIIStart: 16, PIIEnd: 19},
				{Text: "Expiry:         12/2026"},
			},
		},
		{
			ID: "code-editor-secrets", Title: "Code Editor with Secrets", Language: "en",
			BgColor: color.RGBA{R: 40, G: 44, B: 52, A: 255}, FgColor: color.RGBA{R: 171, G: 178, B: 191, A: 255}, FontSize: 13,
			Lines: []textLine{
				{Text: "// config.js"},
				{Text: "const config = {"},
				{Text: "  apiKey: 'sk-ant-api03-abc123def456ghi789jkl012mno345',", IsPII: true, Category: "api_key", PIIStart: 11, PIIEnd: 54},
				{Text: "  dbPassword: 'Sup3r$ecret!DB_2024',", IsPII: true, Category: "password", PIIStart: 15, PIIEnd: 35},
				{Text: "  sessionSecret: 'sess-fixture-example',", IsPII: true, Category: "session_id", PIIStart: 18, PIIEnd: 38},
				{Text: "  host: 'api.production.example.com',"},
				{Text: "  port: 5432,"},
				{Text: "};"},
			},
		},
		{
			ID: "chat-pii-leak", Title: "Chat with PII", Language: "en",
			BgColor: lightBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Alice: Hey, can you check my account?"},
				{Text: "Bob: Sure, what's your email?"},
				{Text: "Alice: It's alice.johnson@example.com", IsPII: true, Category: "private_email", PIIText: "alice.johnson@example.com"},
				{Text: "Bob: And the verification code?"},
				{Text: "Alice: OTP: 483 921", IsPII: true, Category: "one_time_password", PIIStart: 12, PIIEnd: 19},
				{Text: "Bob: Got it. Your password is still"},
				{Text: "password: Tr0ub4dor&3!xQ", IsPII: true, Category: "password", PIIStart: 10, PIIEnd: 24},
				{Text: "Alice: Thanks!"},
			},
		},
		{
			ID: "insurance-form-ja", Title: "Insurance Form (Japanese)", Language: "ja",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 15,
			Lines: []textLine{
				{Text: "=== Insurance Policy Details ==="},
				{Text: ""},
				{Text: "Policy number: INS-2024-JP-00789", IsPII: true, Category: "account_number", PIIStart: 15, PIIEnd: 32},
				{Text: "Insurance company: Tokyo Marine Insurance Co.", IsPII: true, Category: "private_org", PIIStart: 20, PIIEnd: 45},
				{Text: ""},
				{Text: "Phone: +81-00-1234-5678", IsPII: true, Category: "private_phone", PIIStart: 7, PIIEnd: 23},
				{Text: ""},
				{Text: "Vehicle Registration:"},
				{Text: "Number plate: TKY 500 A 12-34"},
			},
		},
		{
			ID: "terminal-ssh-creds", Title: "Terminal SSH Credentials", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 14,
			Lines: []textLine{
				{Text: "$ ssh admin@192.168.1.100"},
				{Text: "admin@192.168.1.100's password: xK9#mP2$vL5@Qw8", IsPII: true, Category: "password", PIIStart: 32, PIIEnd: 47},
				{Text: "Last login: Sun Jun 01 2026 from 10.0.0.1"},
				{Text: ""},
				{Text: "$ cat /etc/secrets/api.key"},
				{Text: "AGE-SECRET-KEY-1QFZWM9TXC7NRNPM6JHJQ6YPXK9E5KNCAGL", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 48},
				{Text: ""},
				{Text: "$ cat ~/.aws/credentials"},
				{Text: "[default]"},
				{Text: "aws_access_key_id = AKIAI44QH8DHBEXAMPLE", IsPII: true, Category: "api_key", PIIStart: 20, PIIEnd: 40},
			},
		},
		{
			ID: "contact-list-zh", Title: "Contact List (Chinese)", Language: "zh",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 15,
			Lines: []textLine{
				{Text: "=== Contact Information ==="},
				{Text: ""},
				{Text: "zhang.wei@example.com", IsPII: true, Category: "private_email", PIIText: "zhang.wei@example.com"},
				{Text: "li.na@example.net", IsPII: true, Category: "private_email", PIIText: "li.na@example.net"},
				{Text: "wang.fang@example.org", IsPII: true, Category: "private_email", PIIText: "wang.fang@example.org"},
				{Text: ""},
				{Text: "Phone: +86 000-0013-8000", IsPII: true, Category: "private_phone", PIIStart: 7, PIIEnd: 24},
				{Text: "Mobile: +86 000-1234-5678", IsPII: true, Category: "private_phone", PIIStart: 8, PIIEnd: 25},
			},
		},
		{
			ID: "env-file-secrets", Title: "Env File", Language: "en",
			BgColor: color.RGBA{R: 30, G: 30, B: 30, A: 255}, FgColor: color.RGBA{R: 206, G: 145, B: 120, A: 255}, FontSize: 13,
			Lines: []textLine{
				{Text: "# .env.production"},
				{Text: "DATABASE_URL=postgres://admin:P@ssw0rd!DB@db.internal:5432/prod"},
				{Text: "P@ssw0rd!DB", IsPII: true, Category: "password", PIIStart: 0, PIIEnd: 11},
				{Text: "STRIPE_SECRET_KEY=sk-test-example-api-key", IsPII: true, Category: "api_key", PIIStart: 0, PIIEnd: 41},
				{Text: "SESSION_SECRET=sess-fixture-example", IsPII: true, Category: "session_id", PIIStart: 0, PIIEnd: 35},
				{Text: "REDIS_URL=redis://default:r3d1sP@ss@cache.internal:6379"},
				{Text: "SENTRY_DSN=https://abc123@o12345.ingest.sentry.io/67890"},
				{Text: "NODE_ENV=production"},
			},
		},
		{
			ID: "address-form-mixed", Title: "Address Form Mixed", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Shipping Address:"},
				{Text: "1234 Oak Ave, Springfield, IL 62704", IsPII: true, Category: "street_address", PIIStart: 0, PIIEnd: 35},
				{Text: ""},
				{Text: "Billing Address:"},
				{Text: "5678 Maple Blvd, Portland, OR 97201", IsPII: true, Category: "street_address", PIIStart: 0, PIIEnd: 35},
				{Text: ""},
				{Text: "Driver's license: D12345678", IsPII: true, Category: "government_id", PIIStart: 18, PIIEnd: 27},
				{Text: ""},
				{Text: "Order total: $127.50"},
			},
		},
		{
			ID: "device-debug-log", Title: "Device Debug Log", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "[2026-06-01 14:32:01] Device connected"},
				{Text: "IMEI: 353456789012345", IsPII: true, Category: "device_id", PIIStart: 6, PIIEnd: 21},
				{Text: "IDFA: 6D92078A-8246-4BA4-AE5B-76104861E7DC", IsPII: true, Category: "device_id", PIIStart: 6, PIIEnd: 42},
				{Text: ""},
				{Text: "[2026-06-01 14:32:02] Auth token refreshed"},
				{Text: "session_id=prod_sess_a1b2c3d4e5f6g7h8", IsPII: true, Category: "session_id", PIIStart: 11, PIIEnd: 37},
				{Text: "refresh_token=rt_xYzAbCdEfGhIjKlMnOpQ", IsPII: true, Category: "session_id", PIIStart: 14, PIIEnd: 38},
				{Text: ""},
				{Text: "[2026-06-01 14:32:03] Sync complete, 42 items"},
			},
		},
		{
			ID: "browser-checkout-autofill", Title: "Checkout Form with Autofill", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Chrome - checkout.example.test"},
				{Text: ""},
				{Text: "Contact email: maya.chen@customer.example", IsPII: true, Category: "private_email", PIIText: "maya.chen@customer.example"},
				{Text: "Mobile phone: +1 (212) 555-0184", IsPII: true, Category: "private_phone", PIIText: "+1 (212) 555-0184"},
				{Text: "Shipping address"},
				{Text: "44 Pine St, New York, NY 10013", IsPII: true, Category: "street_address", PIIText: "44 Pine St, New York, NY 10013"},
				{Text: ""},
				{Text: "Autofill suggestions"},
				{Text: "  Visa ending 4242"},
				{Text: "  Card number: 4242 4242 4242 4242", IsPII: true, Category: "credit_card", PIIText: "4242 4242 4242 4242"},
				{Text: "  CVV: 737", IsPII: true, Category: "cvv", PIIText: "737"},
			},
		},
		{
			ID: "slack-incident-credentials", Title: "Slack Incident with Credentials", Language: "en",
			BgColor: color.RGBA{R: 25, G: 26, B: 28, A: 255}, FgColor: color.RGBA{R: 232, G: 232, B: 232, A: 255}, FontSize: 13,
			Lines: []textLine{
				{Text: "#incident-prod-auth"},
				{Text: "SRE: paging owner alex.ops@example.com", IsPII: true, Category: "private_email", PIIText: "alex.ops@example.com"},
				{Text: "Bot: OTP 492-817 expires in 4 minutes", IsPII: true, Category: "one_time_password", PIIText: "492-817"},
				{Text: "Dev: reset_token=rt-fixture-example", IsPII: true, Category: "session_id", PIIText: "rt-fixture-example"},
				{Text: "Dev: signed log bundle:"},
				{Text: "https://files.example.com/incidents/auth.log?signature=1f2e3d4c5b6a7980", IsPII: true, Category: "private_link", PIIText: "https://files.example.com/incidents/auth.log?signature=1f2e3d4c5b6a7980"},
				{Text: "SRE: root cause looks like expired JWKS cache"},
			},
		},
		{
			ID: "github-actions-log-secrets", Title: "GitHub Actions Log", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "Run deploy-production"},
				{Text: "::group::Load cloud credentials"},
				{Text: "AWS_ACCESS_KEY_ID=AKIAZZZZZZZZZZZZZZZZ", IsPII: true, Category: "api_key", PIIText: "AKIAZZZZZZZZZZZZZZZZ"},
				{Text: "DATABASE_PASSWORD=ProdDb!2026#ChangeMe", IsPII: true, Category: "password", PIIText: "ProdDb!2026#ChangeMe"},
				{Text: "SESSION_ID=prod_sess_9f8e7d6c5b4a3210", IsPII: true, Category: "session_id", PIIText: "prod_sess_9f8e7d6c5b4a3210"},
				{Text: "Uploading artifact to:"},
				{Text: "https://s3.amazonaws.com/acme-artifacts/report.csv?X-Amz-Signature=abcdef1234567890", IsPII: true, Category: "private_link", PIIText: "https://s3.amazonaws.com/acme-artifacts/report.csv?X-Amz-Signature=abcdef1234567890"},
				{Text: "::endgroup::"},
			},
		},
		{
			ID: "notion-crm-record", Title: "Notion CRM Record", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Notion - Customer Renewal Notes"},
				{Text: ""},
				{Text: "Account number: ACCT-2026-88421", IsPII: true, Category: "account_number", PIIText: "ACCT-2026-88421"},
				{Text: "Primary email: priya.rao@example.com", IsPII: true, Category: "private_email", PIIText: "priya.rao@example.com"},
				{Text: "Phone: +44 20 7946 0100", IsPII: true, Category: "private_phone", PIIText: "+44 20 7946 0100"},
				{Text: "Billing address: 18 King St, London SW1A 1AA", IsPII: true, Category: "street_address", PIIText: "18 King St, London SW1A 1AA"},
				{Text: "Renewal risk: medium"},
				{Text: "Next step: send updated MSA by Friday"},
			},
		},
		{
			ID: "spreadsheet-customer-accounts", Title: "Spreadsheet Customer Accounts", Language: "en",
			BgColor: color.RGBA{R: 250, G: 252, B: 250, A: 255}, FgColor: darkFg, FontSize: 13,
			Lines: []textLine{
				{Text: "Numbers - Customer Export"},
				{Text: "Name              Account No.        Email                    Phone"},
				{Text: "Jordan Lee        Account No: CUST-884211   jordan.lee@example.com   Phone: +1 646 555 0144", PIISpans: []piiSpan{
					{Category: "account_number", Text: "CUST-884211"},
					{Category: "private_email", Text: "jordan.lee@example.com"},
					{Category: "private_phone", Text: "+1 646 555 0144"},
				}},
				{Text: "Casey Park        Account No: CUST-771029   casey.park@example.net   Phone: +1 415 555 0161", PIISpans: []piiSpan{
					{Category: "account_number", Text: "CUST-771029"},
					{Category: "private_email", Text: "casey.park@example.net"},
					{Category: "private_phone", Text: "+1 415 555 0161"},
				}},
				{Text: "Quarterly ARR     125000             Renewal stage            Legal"},
			},
		},
		{
			ID: "low-contrast-login-form", Title: "Low Contrast Login Form", Language: "en",
			BgColor: color.RGBA{R: 242, G: 244, B: 247, A: 255}, FgColor: color.RGBA{R: 108, G: 118, B: 128, A: 255}, FontSize: 12,
			Lines: []textLine{
				{Text: "Internal Admin Login"},
				{Text: "Username: robin.admin@example.com", IsPII: true, Category: "private_email", PIIText: "robin.admin@example.com"},
				{Text: "Password: AdminPortal#2026!", IsPII: true, Category: "password", PIIText: "AdminPortal#2026!"},
				{Text: "2FA code: 831 204", IsPII: true, Category: "one_time_password", PIIText: "831 204"},
				{Text: "Remember this device for 30 days"},
			},
		},
		{
			ID: "ocr-fragmented-secrets", Title: "OCR Fragmented Secrets", Language: "en",
			BgColor: color.RGBA{R: 252, G: 252, B: 253, A: 255}, FgColor: darkFg, FontSize: 13,
			Lines: []textLine{
				{Text: "OCR fragments captured from a dense support/debug window"},
				{Text: ""},
				{Text: "Email: alex . ops @ example . com", IsPII: true, Category: "private_email", PIIText: "alex . ops @ example . com"},
				{Text: "Phone: +1 ( 415 ) 555 - 0198", IsPII: true, Category: "private_phone", PIIText: "+1 ( 415 ) 555 - 0198"},
				{Text: "Card number: 4242 4242 4242 4242", IsPII: true, Category: "credit_card", PIIText: "4242 4242 4242 4242"},
				{Text: "2FA code: 831 204", IsPII: true, Category: "one_time_password", PIIText: "831 204"},
				{Text: "session_id = prod_sess_7f6e5d4c3b2a1098", IsPII: true, Category: "session_id", PIIText: "prod_sess_7f6e5d4c3b2a1098"},
				{Text: "OPENAI_API_KEY = sk-test-example-api-key", IsPII: true, Category: "api_key", PIIText: "sk-test-example-api-key"},
				{Text: "Password: Admin Portal # 2026 !", IsPII: true, Category: "password", PIIText: "Admin Portal # 2026 !"},
				{Text: "Account No : CUST - 884211", IsPII: true, Category: "account_number", PIIText: "CUST - 884211"},
				{Text: ""},
				{Text: "Clean row: release 2026.06.07 passed in 42.1s"},
			},
		},
		{
			ID: "email-refund-thread", Title: "Email Refund Thread", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Gmail - Refund request from customer"},
				{Text: "From: Olivia Park <olivia.park@example.org>", IsPII: true, Category: "private_email", PIIText: "olivia.park@example.org"},
				{Text: "To: support@acme.example"},
				{Text: "Order account: ACCT-778812"},
				{Text: "Please call me at +1 917 555 0188 before refunding the card.", IsPII: true, Category: "private_phone", PIIText: "+1 917 555 0188"},
				{Text: "Card ending 9903, billing ZIP 10013."},
				{Text: "Attachment link: https://files.example.com/refunds/claim.pdf?token=example-token", IsPII: true, Category: "private_link", PIIText: "https://files.example.com/refunds/claim.pdf?token=example-token"},
			},
		},
		{
			ID: "browser-bank-dashboard", Title: "Bank Dashboard", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Online Banking - Account Overview"},
				{Text: ""},
				{Text: "Checking Account No: CHK-004918273645", IsPII: true, Category: "account_number", PIIText: "CHK-004918273645"},
				{Text: "Routing number: 021000021"},
				{Text: "Debit card: 4532 1145 9876 2301", IsPII: true, Category: "credit_card", PIIText: "4532 1145 9876 2301"},
				{Text: "Secure message email: dana.kim@example.com", IsPII: true, Category: "private_email", PIIText: "dana.kim@example.com"},
				{Text: "Support phone on file: +1 (305) 555-0109", IsPII: true, Category: "private_phone", PIIText: "+1 (305) 555-0109"},
			},
		},
		{
			ID: "medical-portal-record", Title: "Medical Portal Record", Language: "en",
			BgColor: color.RGBA{R: 247, G: 250, B: 252, A: 255}, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Patient Portal - Visit Summary"},
				{Text: ""},
				{Text: "Member ID: HMO-2026-550018", IsPII: true, Category: "account_number", PIIText: "HMO-2026-550018"},
				{Text: "Patient email: nora.lee@example.com", IsPII: true, Category: "private_email", PIIText: "nora.lee@example.com"},
				{Text: "Phone: +1 206 555 0172", IsPII: true, Category: "private_phone", PIIText: "+1 206 555 0172"},
				{Text: "Insurance policy number: POL-US-883420", IsPII: true, Category: "account_number", PIIText: "POL-US-883420"},
				{Text: "Next appointment: dermatology follow-up"},
			},
		},
		{
			ID: "support-ticket-sidebar", Title: "Support Ticket Sidebar", Language: "en",
			BgColor: color.RGBA{R: 252, G: 252, B: 253, A: 255}, FgColor: darkFg, FontSize: 13,
			Lines: []textLine{
				{Text: "Zendesk - Ticket #883421"},
				{Text: "Requester: Renata Sousa"},
				{Text: "Email: renata.sousa@example.com", IsPII: true, Category: "private_email", PIIText: "renata.sousa@example.com"},
				{Text: "Phone: +55 11 00000 0199", IsPII: true, Category: "private_phone", PIIText: "+55 11 00000 0199"},
				{Text: "Contract number: BR-CNTR-2026-4412", IsPII: true, Category: "account_number", PIIText: "BR-CNTR-2026-4412"},
				{Text: "Private upload: https://uploads.example.com/tickets/883421?signature=abc123def4567890", IsPII: true, Category: "private_link", PIIText: "https://uploads.example.com/tickets/883421?signature=abc123def4567890"},
				{Text: "Agent note: customer requested DPA copy"},
			},
		},
		{
			ID: "ai-chat-secret-review", Title: "AI Chat Secret Review", Language: "en",
			BgColor: color.RGBA{R: 32, G: 33, B: 36, A: 255}, FgColor: color.RGBA{R: 232, G: 234, B: 237, A: 255}, FontSize: 13,
			Lines: []textLine{
				{Text: "User: Can you review this failed deploy log?"},
				{Text: "DATABASE_URL=postgres://app:BlueBird!2026@db.internal:5432/prod"},
				{Text: "BlueBird!2026", IsPII: true, Category: "password", PIIText: "BlueBird!2026"},
				{Text: "OPENAI_API_KEY=sk-test-example-api-key", IsPII: true, Category: "api_key", PIIText: "sk-test-example-api-key"},
				{Text: "refresh_token=rt_chat_9z8y7x6w5v4u3t2s1r0q", IsPII: true, Category: "session_id", PIIText: "rt_chat_9z8y7x6w5v4u3t2s1r0q"},
				{Text: "Assistant: I can explain the error, but rotate those credentials first."},
			},
		},
		{
			ID: "browser-dev-console-auth", Title: "Browser Dev Console Auth", Language: "en",
			BgColor: color.RGBA{R: 28, G: 29, B: 32, A: 255}, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "Chrome DevTools - Console"},
				{Text: "> localStorage.getItem('auth_token')"},
				{Text: "\"eyJhbGciOiJIUzI1NiJ9.e30.example\"", IsPII: true, Category: "session_id", PIIText: "eyJhbGciOiJIUzI1NiJ9.e30.example"},
				{Text: "> document.cookie"},
				{Text: "sid=sess_web_1a2b3c4d5e6f7g8h9i0j; theme=dark", IsPII: true, Category: "session_id", PIIText: "sess_web_1a2b3c4d5e6f7g8h9i0j"},
				{Text: "> fetch('/api/profile')"},
				{Text: "200 OK"},
			},
		},
		{
			ID: "clean-calendar-product-plan", Title: "Calendar Product Plan", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Calendar - Product planning"},
				{Text: "10:00 Roadmap review"},
				{Text: "11:30 Design critique"},
				{Text: "Room: Conf 204"},
				{Text: "Meet link: https://meet.example.com/public/product-sync"},
				{Text: "Notes: discuss metrics dashboard and onboarding copy"},
			},
		},
		{
			ID: "clean-ci-log-public", Title: "Clean CI Log Public", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "Run unit tests"},
				{Text: "PASS ./internal/pii/eval 0.42s"},
				{Text: "coverage: 82.4% of statements"},
				{Text: "artifact: build-2026-06-03T09-15-00Z.zip"},
				{Text: "commit: 9fceb02c1a4f93b5d0a7b2c7e2f14aa9f8d6c0ab"},
				{Text: "public URL: https://docs.example.com/release-notes/v1.8"},
			},
		},
		{
			ID: "clean-terminal-ops", Title: "Terminal Ops (No PII)", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "$ kubectl get pods -n production"},
				{Text: "api-7f96c5d8f9-ptm4x     1/1     Running     0     12d"},
				{Text: "worker-5d789f7c8b-nm2ql  1/1     Running     0     12d"},
				{Text: "$ git rev-parse HEAD"},
				{Text: "8f14e45fceea167a5a36dedd4bea2543"},
				{Text: "$ curl https://status.example.com/public/health"},
				{Text: "{\"status\":\"ok\",\"version\":\"2026.06.01\",\"latency_ms\":42}"},
			},
		},
		{
			ID: "clean-browser-docs-public-links", Title: "Browser Docs Public Links", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "Documentation - public API examples"},
				{Text: "GET https://api.example.com/v1/projects/{project_id}"},
				{Text: "Example UUID: 550e8400-e29b-41d4-a716-446655440000"},
				{Text: "Example version: v2.4.18"},
				{Text: "Public docs: https://docs.example.com/guides/authentication"},
				{Text: "Issue reference: PROJ-1842"},
				{Text: "Price example: $249.99"},
			},
		},
		{
			ID: "clean-dashboard", Title: "Dashboard (No PII)", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "=== System Dashboard ==="},
				{Text: ""},
				{Text: "CPU Usage: 45%    Memory: 8.2 GB / 16 GB"},
				{Text: "Disk:  234 GB free of 512 GB"},
				{Text: "Network: 1.2 Mbps up / 45.6 Mbps down"},
				{Text: ""},
				{Text: "Active Connections: 1,234"},
				{Text: "Requests/sec: 5,678"},
				{Text: "Error Rate: 0.02%"},
				{Text: "Uptime: 45 days 12 hours"},
			},
		},
		{
			ID: "clean-git-log", Title: "Git Log (No PII)", Language: "en",
			BgColor: darkBg, FgColor: lightFg, FontSize: 13,
			Lines: []textLine{
				{Text: "$ git log --oneline -5"},
				{Text: "abc1234 fix(auth): handle token refresh edge case"},
				{Text: "def5678 feat(ui): add dark mode toggle"},
				{Text: "ghi9012 chore(deps): bump react to 18.3.1"},
				{Text: "jkl3456 docs: update API reference"},
				{Text: "mno7890 test: add integration tests for /users"},
				{Text: ""},
				{Text: "$ git status"},
				{Text: "On branch feature/dashboard-v2"},
				{Text: "nothing to commit, working tree clean"},
			},
		},
		{
			ID: "clean-code", Title: "Clean Code (No PII)", Language: "en",
			BgColor: color.RGBA{R: 40, G: 44, B: 52, A: 255}, FgColor: color.RGBA{R: 171, G: 178, B: 191, A: 255}, FontSize: 13,
			Lines: []textLine{
				{Text: "func processOrder(ctx context.Context, id string) error {"},
				{Text: "    order, err := s.repo.GetOrder(ctx, id)"},
				{Text: "    if err != nil {"},
				{Text: "        return fmt.Errorf(\"get order %s: %w\", id, err)"},
				{Text: "    }"},
				{Text: "    if order.Status != StatusPending {"},
				{Text: "        return ErrOrderNotPending"},
				{Text: "    }"},
				{Text: "    return s.repo.UpdateStatus(ctx, id, StatusProcessing)"},
				{Text: "}"},
			},
		},
		{
			ID: "license-plates-mixed", Title: "Vehicle Records", Language: "zh",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 15,
			Lines: []textLine{
				{Text: "=== Vehicle Registry ==="},
				{Text: ""},
				{Text: "Record 1:"},
				{Text: "Plate: JingA12345"},
				{Text: "Record 2:"},
				{Text: "Plate: YueB88888"},
				{Text: "Record 3:"},
				{Text: "Plate: HuC12D34"},
				{Text: ""},
				{Text: "Total vehicles: 3"},
			},
		},
		{
			ID: "payment-receipt", Title: "Payment Receipt", Language: "en",
			BgColor: whiteBg, FgColor: darkFg, FontSize: 14,
			Lines: []textLine{
				{Text: "=== Payment Confirmation ==="},
				{Text: ""},
				{Text: "Transaction ID: TXN-2024-0012345"},
				{Text: "Date: 2026-06-01 14:30:00 UTC"},
				{Text: ""},
				{Text: "Card: 5425-2334-3010-9903", IsPII: true, Category: "credit_card", PIIStart: 6, PIIEnd: 25},
				{Text: "Amount: $249.99"},
				{Text: ""},
				{Text: "Billing email: pay@example.com", IsPII: true, Category: "private_email", PIIText: "pay@example.com"},
				{Text: "Phone: +1 (650) 555-0147", IsPII: true, Category: "private_phone", PIIStart: 7, PIIEnd: 24},
			},
		},
	}
}

func renderScenario(sc scenario, ft *opentype.Font, outPath string, quality int) []eval.Annotation {
	img := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))

	// Fill background
	for y := 0; y < imgHeight; y++ {
		for x := 0; x < imgWidth; x++ {
			img.Set(x, y, sc.BgColor)
		}
	}

	face, err := opentype.NewFace(ft, &opentype.FaceOptions{
		Size:    sc.FontSize,
		DPI:     144,
		Hinting: font.HintingFull,
	})
	if err != nil {
		log.Fatalf("create face: %v", err)
	}
	defer face.Close()

	metrics := face.Metrics()
	lineHeight := (metrics.Height).Ceil() + 4
	startX := 60
	startY := 80

	var annotations []eval.Annotation

	for i, line := range sc.Lines {
		if line.Text == "" {
			continue
		}

		y := startY + i*lineHeight
		if y > imgHeight-40 {
			break
		}

		// Draw text
		drawer := &font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(sc.FgColor),
			Face: face,
			Dot:  fixed.Point26_6{X: fixed.I(startX), Y: fixed.I(y)},
		}
		drawer.DrawString(line.Text)

		// Calculate bounding box for PII region
		if line.IsPII && line.Category != "" {
			piiText := line.Text[line.PIIStart:line.PIIEnd]

			// Measure character positions
			prefix := line.Text[:line.PIIStart]
			prefixWidth := font.MeasureString(face, prefix)
			piiWidth := font.MeasureString(face, piiText)

			piiX := float64(startX) + float64(prefixWidth.Ceil())
			piiY := float64(y) - float64(metrics.Ascent.Ceil())
			piiW := float64(piiWidth.Ceil())
			piiH := float64(lineHeight)

			annotations = append(annotations, eval.Annotation{
				Text:     piiText,
				Category: line.Category,
				X:        piiX / float64(imgWidth),
				Y:        piiY / float64(imgHeight),
				Width:    piiW / float64(imgWidth),
				Height:   piiH / float64(imgHeight),
			})
		}
		for _, span := range line.PIISpans {
			piiText := line.Text[span.Start:span.End]

			prefix := line.Text[:span.Start]
			prefixWidth := font.MeasureString(face, prefix)
			piiWidth := font.MeasureString(face, piiText)

			piiX := float64(startX) + float64(prefixWidth.Ceil())
			piiY := float64(y) - float64(metrics.Ascent.Ceil())
			piiW := float64(piiWidth.Ceil())
			piiH := float64(lineHeight)

			annotations = append(annotations, eval.Annotation{
				Text:     piiText,
				Category: span.Category,
				X:        piiX / float64(imgWidth),
				Y:        piiY / float64(imgHeight),
				Width:    piiW / float64(imgWidth),
				Height:   piiH / float64(imgHeight),
			})
		}
	}

	f, err := os.Create(outPath)
	if err != nil {
		log.Fatalf("create image: %v", err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		log.Fatalf("encode jpeg: %v", err)
	}

	return annotations
}
