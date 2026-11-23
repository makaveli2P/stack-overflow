package tui

// batch5Fixtures are the screens of the batch 5 buy-in picker.
func batch5Fixtures() []Fixture {
	return []Fixture{fxWatchWithBank(), fxPickerBigBank(), fxPickerTopUp(), fxBustFromBank(), fxBustTopUp()}
}

// fxWatchWithBank is a spectator with chips in the bank, before S.
func fxWatchWithBank() Fixture {
	f := spectator()
	f.Name = "watching with a bank of 3,450"
	withMoney(&f.Snap)
	f.Snap.Me.Bank = 3450
	return f
}

// fxPickerBigBank is the picker when the bank holds more than one
// buy-in: option 1 leaves the rest in the bank.
func fxPickerBigBank() Fixture {
	f := fxWatchWithBank()
	f.Name = "buy-in picker, bank larger than a buy-in"
	f.Keys = []string{"s"}
	return f
}

// fxPickerTopUp is the picker when the bank holds less than one buy-in:
// option 1 buys the difference.
func fxPickerTopUp() Fixture {
	f := fxWatchWithBank()
	f.Name = "buy-in picker, bank smaller than a buy-in (top-up)"
	f.Snap.Me.Bank = 300
	f.Keys = []string{"s", "down"}
	return f
}

// fxBustFromBank is a busted player whose bank pays the whole rebuy.
func fxBustFromBank() Fixture {
	f := busted()
	f.Name = "busted with a bank that covers the rebuy"
	withMoney(&f.Snap)
	f.Snap.Me.Bank = 5000
	return f
}

// fxBustTopUp is a busted player whose bank pays only a part of the rebuy.
func fxBustTopUp() Fixture {
	f := busted()
	f.Name = "busted with a bank that covers part of the rebuy"
	withMoney(&f.Snap)
	f.Snap.Me.Bank = 300
	return f
}
