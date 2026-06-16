package portrayal

import "testing"

func TestFormatSubstitute(t *testing.T) {
	attrs := map[string]interface{}{"VERCOP": 12.34}
	// The Spa Creek drawbridge label: TE('clr op %4.1lf','VERCOP',...).
	got, ok := formatSubstitute(attrs, "clr op %4.1lf", []string{"VERCOP"})
	if !ok || got != "clr op 12.3" {
		t.Errorf("formatSubstitute = %q ok=%v, want \"clr op 12.3\"", got, ok)
	}
	// %s passes text through.
	got, ok = formatSubstitute(map[string]interface{}{"OBJNAM": "Spa Creek"}, "Nr %s", []string{"OBJNAM"})
	if !ok || got != "Nr Spa Creek" {
		t.Errorf("got %q ok=%v, want \"Nr Spa Creek\"", got, ok)
	}
	// Missing attribute suppresses the whole label.
	if _, ok := formatSubstitute(attrs, "by %s", []string{"OBJNAM"}); ok {
		t.Error("missing attribute should suppress the label (ok=false)")
	}
	// Literal %% and no-attr format.
	if got, _ := formatSubstitute(attrs, "100%%", nil); got != "100%" {
		t.Errorf("got %q, want 100%%", got)
	}
}

func TestMapJustification(t *testing.T) {
	// HJUST: 1 centre, 2 right, else left.
	if mapHJust(1) != HAlignCenter || mapHJust(2) != HAlignRight || mapHJust(3) != HAlignLeft {
		t.Error("HJUST mapping wrong")
	}
	// VJUST: 2 centre, 3 top, else bottom.
	if mapVJust(2) != VAlignMiddle || mapVJust(3) != VAlignTop || mapVJust(1) != VAlignBottom {
		t.Error("VJUST mapping wrong")
	}
}
