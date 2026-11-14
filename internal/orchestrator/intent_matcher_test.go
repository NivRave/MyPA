package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntentMatcher_EnglishDomains(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	tests := []struct {
		name     string
		input    string
		expected DomainGroup
	}{
		{"Baby - feed", "Baby drank 120ml of formula", DomainBaby},
		{"Baby - diaper", "Need to change a dirty diaper", DomainBaby},
		{"Finance - expense", "I spent 45 dollars at the grocery store", DomainFinance},
		{"Finance - budget", "Show me our monthly budget report", DomainFinance},
		{"House - maintenance", "Fix the AC filter in the apartment", DomainHouse},
		{"House - vehicle", "The car needs an oil change and annual test", DomainHouse},
		{"Calendar - meeting", "Schedule a meeting tomorrow at 3pm", DomainCalendar},
		{"Gmail - email", "Search unread email in my inbox", DomainGmail},
		{"Tasks - todo", "Add buy milk to my task list", DomainTasks},
		{"Contacts - crm", "Search contacts for Dr. Smith phone number", DomainContacts},
		{"Automation - rule", "Create an automation rule for webhook", DomainAutomation},
		{"Shared - family", "Create a shared reminder for my spouse", DomainShared},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := matcher.MatchDomains(tt.input)
			assert.Contains(t, matched, tt.expected, "Expected %s to match domain %s", tt.input, tt.expected)
		})
	}
}

func TestIntentMatcher_HebrewPrefixes(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	tests := []struct {
		name     string
		input    string
		expected DomainGroup
	}{
		{"Hebrew Baby - האכלה", "האכלתי את התינוק 120 מ\"ל", DomainBaby},
		{"Hebrew Baby - חיתול", "החלפתי טיטול רטוב", DomainBaby},
		{"Hebrew Finance - הוצאה", "שילמתי 150 ש\"ח בסופר", DomainFinance},
		{"Hebrew Finance - תקציב", "מה המצב של התקציב החודשי", DomainFinance},
		{"Hebrew House - מזגן", "צריך לנקות את הפילטר של המזגן בדירה", DomainHouse},
		{"Hebrew House - רכב", "מתי הטסט של הרכב", DomainHouse},
		{"Hebrew Calendar - יומן", "לקבוע פגישה ביומן ליום שלישי", DomainCalendar},
		{"Hebrew Gmail - מייל", "תבדוק לי מיילים שלא נקראו", DomainGmail},
		{"Hebrew Tasks - משימות", "תוסיף משימה לרשימת קניות", DomainTasks},
		{"Hebrew Contacts - איש קשר", "תמצא לי את המספר טלפון של יוסי", DomainContacts},
		{"Hebrew Automation - כלל", "תיצור כלל אוטומציה חדש", DomainAutomation},
		{"Hebrew Shared - שיתוף", "תזכורת משותפת עם בת זוג", DomainShared},
		{"Hebrew Vav prefix - והוצאה", "הייתה לנו עוד הכנסה והוצאה אתמול", DomainFinance},
		{"Hebrew Compound prefix - ובתינוק", "צריך לטפל בבית ובתינוק היום", DomainBaby},
		{"Hebrew Compound prefix - ולקבוע", "רציתי לבדוק זמינות ולקבוע פגישה", DomainCalendar},
		{"Hebrew Vav prefix - ושילמתי", "קניתי מצרכים ושילמתי במזומן", DomainFinance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := matcher.MatchDomains(tt.input)
			assert.Contains(t, matched, tt.expected, "Expected %s to match domain %s", tt.input, tt.expected)
		})
	}
}

func TestIntentMatcher_MultiDomain(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	// Baby + Calendar
	matched := matcher.MatchDomains("Liam has a pediatrician appointment tomorrow at 10:00")
	assert.Contains(t, matched, DomainBaby)
	assert.Contains(t, matched, DomainCalendar)

	// House + Finance
	matched = matcher.MatchDomains("Paid 500 ILS to the plumber for apartment repair")
	assert.Contains(t, matched, DomainFinance)
	assert.Contains(t, matched, DomainHouse)
}

func TestIntentMatcher_AmbiguousOrNoMatch(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	matched := matcher.MatchDomains("Hello, good morning! How are you today?")
	assert.Empty(t, matched, "Ambiguous greeting should not trigger domain tools")
}

func BenchmarkMatchDomains(b *testing.B) {
	matcher := NewKeywordIntentMatcher()
	query := "Liam ate 120ml of formula, please schedule a pediatrician checkup at 10am"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := matcher.MatchDomains(query)
		require.NotEmpty(b, res)
	}
}
