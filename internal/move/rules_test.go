package move

import "testing"

// No fixtures are written here, so no testguard TestMain is needed.
func TestParseRulesRefusesEscapes(t *testing.T) {
	for _, bad := range []string{"MP4=..", "MP4=../x", "MP4=x/../../y", "MP4=/abs"} {
		if _, err := ParseRules([]string{bad}); err == nil {
			t.Errorf("ParseRules(%q) accepted a rule that leaves the destination root", bad)
		}
	}
	for _, ok := range []string{"MP4=Videos", "JPG=Photos/2024", "MP4=v..ideos"} {
		if _, err := ParseRules([]string{ok}); err != nil {
			t.Errorf("ParseRules(%q): %v", ok, err)
		}
	}
}
