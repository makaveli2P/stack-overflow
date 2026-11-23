package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Palette. Adaptive colours change with the client's background.
var (
	colText   = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
	colDim    = lipgloss.AdaptiveColor{Light: "#656d76", Dark: "#8b949e"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#afb8c1", Dark: "#484f58"}
	colFelt   = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#2ea043"}
	colGold   = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#f2cc60"}
	colChip   = lipgloss.AdaptiveColor{Light: "#bc4c00", Dark: "#f0883e"}
	colWin    = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#56d364"}
	colErr    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#ff7b72"}
	colMe     = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	colInfo   = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#d2a8ff"}
	colCardBg = lipgloss.Color("#f6f8fa")
	colCardFg = lipgloss.Color("#1f2328")
	colCardRd = lipgloss.Color("#cf222e")
	colBackBg = lipgloss.Color("#1f4f99")
	colBackFg = lipgloss.Color("#79a6f2")
	colKeyBg  = lipgloss.AdaptiveColor{Light: "#d0d7de", Dark: "#30363d"}
	// colChatHL is the soft background of a new chat message. Each colour
	// profile gets its own value, so the tint stays soft with 256 or 16
	// colours.
	colChatHL = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#fff8c5", ANSI256: "230", ANSI: "11"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#3b3214", ANSI256: "58", ANSI: "8"},
	}
)

// nameColors colour player names in chat, so each person is easy to follow.
var nameColors = []lipgloss.AdaptiveColor{
	{Light: "#0969da", Dark: "#79c0ff"},
	{Light: "#8250df", Dark: "#d2a8ff"},
	{Light: "#bf3989", Dark: "#ff9bce"},
	{Light: "#1a7f37", Dark: "#7ee787"},
	{Light: "#9a6700", Dark: "#e3b341"},
	{Light: "#bc4c00", Dark: "#ffa657"},
	{Light: "#0550ae", Dark: "#a5d6ff"},
}

// styles holds every style, built once from the session's renderer.
type styles struct {
	r *lipgloss.Renderer

	text, dim, faint, bold    lipgloss.Style
	gold, bet, win, err, me   lipgloss.Style
	info                      lipgloss.Style
	title, redSuit            lipgloss.Style
	key, keyLabel             lipgloss.Style
	cardRed, cardBlack, back  lipgloss.Style
	cardEdge, cardEdgeWin     lipgloss.Style
	slotEdge                  lipgloss.Style
	badgeD, badgeBlind, away  lipgloss.Style
	allIn                     lipgloss.Style
	seat, seatEmpty           lipgloss.Style
	felt, panel               lipgloss.Style
	tabOn, tabOff, tabUnread  lipgloss.Style
	inputBox, inputIdle       lipgloss.Style
	brand, cursor             lipgloss.Style
	rabbitEdge                lipgloss.Style
	rabbitFace, shown         lipgloss.Style
	logStreet, logWin, logHdr lipgloss.Style
	chatHL, chatMark, tabLit  lipgloss.Style // a new chat message (batch 11)

	// noColor is true for a terminal with no colour. lipgloss then draws
	// no style at all, also no bold.
	noColor bool
}

// SGR codes for bold. A terminal with no colour can still show bold, but
// lipgloss draws no style for it. bold uses these codes directly.
const (
	sgrBold    = "\x1b[1m"
	sgrBoldOff = "\x1b[22m"
)

// bold draws plain text in bold for a terminal with no colour.
func bold(text string) string { return sgrBold + text + sgrBoldOff }

var dashedBorder = lipgloss.Border{
	Top: "┄", Bottom: "┄", Left: "┆", Right: "┆",
	TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
}

func newStyles(r *lipgloss.Renderer) styles {
	n := r.NewStyle
	s := styles{r: r}
	s.text = n().Foreground(colText)
	s.dim = n().Foreground(colDim)
	s.faint = n().Foreground(colFaint)
	s.bold = n().Foreground(colText).Bold(true)
	s.gold = n().Foreground(colGold).Bold(true)
	s.bet = n().Foreground(colChip).Bold(true)
	s.win = n().Foreground(colWin).Bold(true)
	s.err = n().Foreground(colErr).Bold(true)
	s.me = n().Foreground(colMe).Bold(true)
	s.info = n().Foreground(colInfo)
	s.title = n().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#1a7f37")).Bold(true).Padding(0, 1)
	s.redSuit = n().Foreground(colErr)
	s.key = n().Foreground(colText).Background(colKeyBg).Bold(true).Padding(0, 1)
	s.keyLabel = n().Foreground(colText)
	s.cardRed = n().Foreground(colCardRd).Background(colCardBg).Bold(true)
	s.cardBlack = n().Foreground(colCardFg).Background(colCardBg).Bold(true)
	s.back = n().Foreground(colBackFg).Background(colBackBg)
	s.cardEdge = n().Foreground(colDim)
	s.cardEdgeWin = n().Foreground(colWin).Bold(true)
	s.slotEdge = n().Foreground(colFaint)
	s.badgeD = n().Foreground(lipgloss.Color("#1f2328")).Background(lipgloss.Color("#f2cc60")).Bold(true)
	s.badgeBlind = n().Foreground(colMe)
	s.away = n().Foreground(colErr)
	s.allIn = n().Foreground(colErr).Bold(true)
	s.seat = n().Border(lipgloss.RoundedBorder()).BorderForeground(colFaint).Padding(0, 1)
	s.seatEmpty = n().Border(dashedBorder).BorderForeground(colFaint).Padding(0, 1)
	s.felt = n().Border(lipgloss.RoundedBorder()).BorderForeground(colFelt).Padding(0, 1)
	s.panel = n().Border(lipgloss.RoundedBorder()).BorderForeground(colFaint).Padding(0, 1)
	s.tabOn = n().Foreground(colText).Bold(true).Underline(true)
	s.tabOff = n().Foreground(colDim)
	s.tabUnread = n().Foreground(colGold).Bold(true)
	s.inputBox = n().Foreground(colText).Background(colKeyBg)
	s.inputIdle = n().Foreground(colDim).Background(colKeyBg)
	s.brand = n().Foreground(colFelt).Bold(true)
	s.cursor = n().Foreground(colGold).Bold(true)
	s.rabbitEdge = n().Foreground(colInfo)
	s.rabbitFace = n().Foreground(colInfo).Italic(true)
	s.shown = n().Foreground(colInfo).Bold(true)
	s.logStreet = n().Foreground(colMe)
	s.logWin = n().Foreground(colWin)
	s.logHdr = n().Foreground(colText).Bold(true)
	// A new chat message gets a soft background. A terminal with no colour
	// shows no background: chatLines and tabBar use bold there.
	s.noColor = r.ColorProfile() == termenv.Ascii
	s.chatHL = n().Background(colChatHL)
	s.chatMark = n().Foreground(colGold).Bold(true).Inherit(s.chatHL)
	s.tabLit = s.tabUnread.Inherit(s.chatHL)
	return s
}

// nameStyle returns a stable colour for a player name.
func (s styles) nameStyle(name string) lipgloss.Style {
	var h uint32
	for _, c := range name {
		h = h*31 + uint32(c)
	}
	return s.r.NewStyle().Foreground(nameColors[h%uint32(len(nameColors))]).Bold(true)
}
