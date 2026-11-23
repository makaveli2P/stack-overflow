package tui

// batch7Fixtures are the screens of the batch 7 panic key.
func batch7Fixtures() []Fixture {
	return []Fixture{fxPanicNotice(), fxDecoy(), fxDecoyMyTurn()}
}

// fxPanicNotice is phase 1 of the panic screen: the one-second notice.
func fxPanicNotice() Fixture {
	f := headsUpPreflop()
	f.Name = "panic notice"
	f.Panic = 1
	return f
}

// fxDecoy is phase 2 of the panic screen for a spectator: only the decoy.
func fxDecoy() Fixture {
	f := spectator()
	f.Name = "panic decoy"
	f.Panic = 2
	return f
}

// fxDecoyMyTurn is the decoy on the viewer's turn: the last line shows the
// seconds left.
func fxDecoyMyTurn() Fixture {
	f := headsUpPreflop()
	f.Name = "panic decoy on my turn"
	f.Panic = 2
	return f
}
