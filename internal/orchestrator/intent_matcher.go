package orchestrator

import (
	"regexp"
	"strings"
	"sync"
)

// DomainGroup identifies a functional cluster of tools.
type DomainGroup string

const (
	DomainBaby       DomainGroup = "baby"
	DomainFinance    DomainGroup = "finance"
	DomainHouse      DomainGroup = "house"
	DomainCalendar   DomainGroup = "calendar"
	DomainGmail      DomainGroup = "gmail"
	DomainTasks      DomainGroup = "tasks"
	DomainContacts   DomainGroup = "contacts"
	DomainAutomation DomainGroup = "automation"
	DomainShared     DomainGroup = "shared"
)

// IntentMatcher inspects text and returns relevant DomainGroups.
type IntentMatcher interface {
	MatchDomains(text string) []DomainGroup
}

// KeywordIntentMatcher provides fast regex and token-based intent classification.
type KeywordIntentMatcher struct {
	patterns map[DomainGroup]*regexp.Regexp
}

// NewKeywordIntentMatcher compiles bilingual keyword dictionaries for high-speed matching.
func NewKeywordIntentMatcher() *KeywordIntentMatcher {
	kim := &KeywordIntentMatcher{
		patterns: make(map[DomainGroup]*regexp.Regexp),
	}
	kim.initPatterns()
	return kim
}

func (m *KeywordIntentMatcher) initPatterns() {
	// Patterns support English keywords and Hebrew prefixes [הובלכמש]? and compounds
	hebrewPrefix := `(?:^|[^\p{L}\p{N}])(?:[הובלכמש]|וכש|וה|וב|ול|ומ|וש|כש|שב|שה)?`
	wordEnd := `(?:[^\p{L}\p{N}]|$)`

	buildRegex := func(words []string) *regexp.Regexp {
		var escaped []string
		for _, w := range words {
			escaped = append(escaped, regexp.QuoteMeta(strings.ToLower(w)))
		}
		raw := "(?i)" + hebrewPrefix + "(?:" + strings.Join(escaped, "|") + ")" + wordEnd
		return regexp.MustCompile(raw)
	}

	m.patterns[DomainBaby] = buildRegex([]string{
		// English
		"baby", "infant", "newborn", "toddler", "child", "pediatrician",
		"feed", "feeding", "formula", "breastfeed", "breastmilk", "nursing", "bottle", "ml", "oz",
		"diaper", "nappy", "poop", "pee", "wet", "dirty",
		"sleep", "nap", "crib", "bedtime", "wake window",
		"milestone", "teething", "wonder weeks", "vaccine", "vaccination", "shots",
		"fever", "temp", "temperature", "tylenol", "motrin", "advil", "paracetamol", "acamol", "novimol",
		"stroller", "wipes", "pacifier",
		// Hebrew
		"תינוק", "תינוקת", "ילד", "ילדה", "פעוט", "רופא ילדים", "טיפת חלב",
		"האכלה", "האכלתי", "אכל", "אכלה", "מטרנה", "סימילאק", "נוטרילון", "הנקה", "הנקתי", "בקבוק", "שאוב", "חלב אם", "מל", "מ\"ל",
		"חיתול", "החלפתי", "קקי", "פיפי", "טיטול",
		"שינה", "שנ\"צ", "שנצ", "ישן", "ישנה", "נרדם", "נרדמה", "התעורר", "התעוררה", "חלון שינה", "עריסה",
		"חיסון", "חיסונים", "קפיצת גדילה", "חום", "טמפרטורה", "אקמול", "נובימול", "אדוויל", "נורופן",
		"מגבונים", "מוצץ", "משקל", "גובה", "אחוזון",
	})

	m.patterns[DomainFinance] = buildRegex([]string{
		// English
		"finance", "money", "expense", "expenses", "spent", "spend", "spending", "cost", "paid", "pay",
		"purchase", "bought", "buy", "receipt", "invoice", "budget", "budgets", "overbudget",
		"bill", "bills", "subscription", "subscriptions", "recurring", "utility", "utilities",
		"income", "salary", "revenue", "shekel", "dollar", "euro", "ils", "usd", "eur", "cash", "credit", "card", "bank",
		// Hebrew
		"כסף", "פיננסים", "הוצאה", "הוצאות", "הוצאתי", "שילמתי", "לשלם", "תשלום", "תשלומים", "קניתי", "קנייה", "רכישה", "קבלה", "חשבונית",
		"תקציב", "חריגה", "תקרה", "מאזן", "חשבון", "חשבונות", "מנוי", "מנויים", "הוראת קבע",
		"חשמל", "ארנונה", "משכורת", "שכר", "תנועה", "תנועות", "סיכום הוצאות", "שקל", "ש\"ח", "דולר", "יורו", "אשראי", "בנק", "כרטיס",
	})

	m.patterns[DomainHouse] = buildRegex([]string{
		// English
		"house", "home", "apartment", "flat", "lease", "rent", "landlord", "tenant", "mortgage", "contract", "warranty", "insurance",
		"maintenance", "repair", "fix", "plumber", "plumbing", "electrician", "hvac", "filter", "air conditioner", "ac",
		"boiler", "water heater", "leak", "appliance", "refrigerator", "fridge", "dishwasher", "washing machine", "dryer", "pest", "exterminator",
		"car", "vehicle", "auto", "automobile", "license plate", "odometer", "mileage", "annual test", "inspection", "mechanic", "garage", "oil change",
		// Hebrew
		"בית", "דירה", "שכירות", "שכר דירה", "שכ\"ד", "בעל בית", "משכיר", "שוכר", "משכנתא", "חוזה", "אחריות", "ביטוח דירה", "פוליסה",
		"תחזוקה", "תיקון", "לתקן", "אינסטלטור", "שרברב", "חשמלאי", "מזגן", "פילטר", "מסנן", "דוד", "דוד שמש", "נזילה",
		"מקרר", "מדיח", "מכונת כביסה", "מייבש", "תנור", "הדברה", "מדביר",
		"רכב", "אוטו", "מכונית", "טסט", "רישוי", "רישיון רכב", "קילומטראז'", "ק\"מ", "מוסך", "טיפול לרכב", "החלפת שמן", "צמיגים", "ביטוח רכב",
	})

	m.patterns[DomainCalendar] = buildRegex([]string{
		// English
		"calendar", "event", "events", "meeting", "meetings", "appointment", "appointments", "schedule", "reschedule", "sync", "block time",
		// Hebrew
		"יומן", "אירוע", "אירועים", "פגישה", "פגישות", "לו\"ז", "לוז", "לקבוע", "לתאם", "לקבוע פגישה", "יומן גוגל", "תזמן",
	})

	m.patterns[DomainGmail] = buildRegex([]string{
		// English
		"email", "emails", "mail", "gmail", "inbox", "unread", "sender", "draft", "send email", "archive", "trash", "label", "labels", "unsubscribe", "newsletter",
		// Hebrew
		"מייל", "מיילים", "אימייל", "דוא\"ל", "דואר", "ג'ימייל", "תיבת דואר", "דואר נכנס", "לא נקראו", "השב", "טיוטה", "שלח מייל", "ארכיון", "תווית", "הסר מרשימת תפוצה",
	})

	m.patterns[DomainTasks] = buildRegex([]string{
		// English
		"task", "tasks", "todo", "todos", "to-do", "to-dos", "task list", "checklist", "groceries", "grocery list", "shopping list",
		// Hebrew
		"משימה", "משימות", "טו-דו", "רשימת משימות", "רשימת קניות", "קניות", "סמן כבוצע", "השלם משימה", "מטלה", "מטלות",
	})

	m.patterns[DomainContacts] = buildRegex([]string{
		// English
		"contact", "contacts", "phone number", "mobile", "address book", "crm", "birthday", "anniversary", "gift idea",
		// Hebrew
		"איש קשר", "אנשי קשר", "מספר טלפון", "טלפון", "נייד", "ספר טלפונים", "יום הולדת", "יומולדת", "מתנה",
	})

	m.patterns[DomainAutomation] = buildRegex([]string{
		// English
		"automation", "automate", "rule", "rules", "workflow", "workflows", "cron", "trigger", "webhook", "api", "connector", "preference", "preferences", "persona", "standing instructions", "quiet hours",
		// Hebrew
		"אוטומציה", "אוטומציות", "כלל", "כללים", "חוק", "חוקים", "וורקפלו", "קרון", "טריגר", "וובהוק", "ממשק", "העדפה", "העדפות", "פרסונה", "הנחיות קבועות", "שעות שקט",
	})

	m.patterns[DomainShared] = buildRegex([]string{
		// English
		"shared", "share", "family", "partner", "spouse", "hand-off", "shared reminder", "shared space", "notify partner", "family group",
		// Hebrew
		"משותף", "לשתף", "שיתוף", "משפחה", "בן זוג", "בת זוג", "תזכורת משותפת", "מרחב משותף", "קבוצה משפחתית",
	})
}

// MatchDomains scans text against compiled domain patterns and returns matching domain groups.
func (m *KeywordIntentMatcher) MatchDomains(text string) []DomainGroup {
	if len(text) == 0 {
		return nil
	}

	normalized := strings.ToLower(text)

	// Fast path for small inputs (< 2KB): evaluate sequentially to avoid goroutine overhead
	if len(normalized) < 2048 {
		var matched []DomainGroup
		for domain, pattern := range m.patterns {
			if pattern.MatchString(normalized) {
				matched = append(matched, domain)
			}
		}
		return matched
	}

	// For larger inputs, evaluate domain regex patterns concurrently across goroutines
	var (
		mu      sync.Mutex
		matched []DomainGroup
		wg      sync.WaitGroup
	)

	wg.Add(len(m.patterns))
	for domain, pattern := range m.patterns {
		d := domain
		p := pattern
		go func() {
			defer wg.Done()
			if p.MatchString(normalized) {
				mu.Lock()
				matched = append(matched, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return matched
}
