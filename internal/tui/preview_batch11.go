package tui

import "github.com/makaveli2p/stack-overflow/internal/server"

// batch11Fixtures are the screens of batch 11: a new chat message from
// another player is highlighted for 5 seconds. The size of the preview
// selects full or compact mode.
func batch11Fixtures() []Fixture {
	return []Fixture{fxChatNew(), fxChatNewLogTab()}
}

// newChatLine is a long message from Priya that wraps in the chat panel.
var newChatLine = server.ChatLine{At: previewNow, Name: "Priya",
	Text: "Sam, you bluff every single river. Next time I call you down, no matter what the board shows."}

// fxChatNew: a new message from Priya on the open chat tab.
func fxChatNew() Fixture {
	f := sixFlopWaiting()
	f.Name = "new chat message highlighted"
	f.newChat = []server.ChatLine{newChatLine}
	return f
}

// fxChatNewLogTab: a new message from Priya while the log tab is open.
// The tab name "Chat" is highlighted.
func fxChatNewLogTab() Fixture {
	f := fxChatNew()
	f.Name = "new chat message, chat tab highlighted, log tab open"
	f.Keys = []string{"tab"}
	return f
}
