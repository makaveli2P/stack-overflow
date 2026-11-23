package tui

import "time"

// batch4Fixtures are the screens of the batch 4 time extensions.
func batch4Fixtures() []Fixture {
	return []Fixture{fxExtended(), fxNoExtensions(), fxExtendEditor()}
}

// fxExtended is the viewer's turn after one extension: the clock is 30 s
// plus 30 s long, so the timer bar measures against 60 s.
func fxExtended() Fixture {
	f := headsUpPreflop()
	f.Name = "my turn after one time extension"
	f.Snap.ClockTotal = 60 * time.Second
	f.Snap.Me.ExtensionsLeft = 2
	clockOn(&f.Snap, 0, 52*time.Second)
	return f
}

// fxNoExtensions is the viewer's turn with every extension used: the bar
// has no E hint.
func fxNoExtensions() Fixture {
	f := nineFlopBet()
	f.Name = "my turn, no time extensions left"
	f.Snap.Me.ExtensionsLeft = 0
	return f
}

// fxExtendEditor is the amount editor with extensions left.
func fxExtendEditor() Fixture {
	f := headsUpPreflop()
	f.Name = "amount editor with time extensions left"
	f.Keys = []string{"r"}
	return f
}
