# Stack Overflow — design

One person runs `poker` on a computer. The other players connect with
`ssh -p 2222 <name>@<host-ip>`. Nobody else installs anything. The game is
No-Limit Texas Hold'em with play chips. One room, one table.

## Packages

| Package | Job | Depends on |
|---|---|---|
| `internal/cards` | Card, Deck (crypto/rand shuffle), hand evaluator | — |
| `internal/engine` | Pure NLHE table state machine. No goroutines, no clock, no I/O. Deterministic with a stacked deck. | cards |
| `internal/server` | Room: one goroutine owns the engine, the turn clock, the chip bank, sessions, chat. | engine |
| `internal/tui` | Bubble Tea model for one SSH session. Draws a `server.Snapshot`; sends `Session` calls. | server, engine, cards |
| `cmd/poker` | Flags, SSH server (charmbracelet/wish), host key, auth, wiring. | all |

The `api.go` files (and `cards/cards.go`, `cards/eval.go`) are the contracts.
Do not change exported signatures in them.

## Poker rules (engine)

- **Seats.** `Config.Seats` (2–10). A player who sits during a hand plays from the next hand.
- **Eligible for a hand:** seated, not sitting out, stack > 0. A hand needs 2 or more.
- **Button.** Before the first hand the button goes to the lowest eligible seat... then each hand it moves clockwise to the next eligible seat (simple moving button; no dead button).
- **Blinds (3+ players).** Small blind = next eligible seat after the button. Big blind = next eligible seat after the small blind. First to act preflop = next eligible seat after the big blind. Postflop, first to act = first live seat after the button.
- **Blinds (heads-up).** The button posts the small blind and acts first preflop. The other player posts the big blind and acts first on every later street.
- **Short blinds.** A player who cannot cover a blind posts all of the stack and is all-in. The amount to call preflop is still the full big blind. The minimum raise preflop is to 2 × big blind.
- **Bet sizes.** The minimum bet is the big blind (or all-in for less). A raise must raise by at least the size of the last full bet or raise on this street (first increment preflop = big blind). Maximum = all-in (no limit).
- **Incomplete all-in raise.** An all-in that raises by less than a full raise does not reopen the betting. A player who already acted and now faces only incomplete raises may call or fold, not raise. The min-raise increment does not change. Several incomplete raises that add up to a full raise do reopen the action (TDA rule).
- **Lone all-in.** When every other live player is all-in, a player may only call or fold (no bet or raise).
- **Round end.** A betting round ends when every player who is not all-in and not folded acted since the last full raise and matched the current bet. Preflop, the big blind gets the option to raise when the action is only calls.
- **Everyone folds.** The last player wins all pots. No cards are shown. Uncalled chips come back first.
- **Uncalled bet.** The part of the highest bet that nobody matched goes back to the bettor before the pots form (event `EvReturn`).
- **All-in run-out.** When at most one player can still bet, and all bets are matched, the engine deals the rest of the board at once and goes to showdown.
- **Side pots.** Build pots from each player's total `Committed`, level by level. Folded players' chips go into the pots, but folded players are not eligible. Each pot goes to the best hand among its eligible players.
- **Showdown.** Every live hand is shown (office game: no mucking). Order: last aggressor on the river first, else first live seat after the button.
- **Split pots.** Equal shares. The odd chips go one at a time to the winners in seat order, starting at the first seat after the button.
- **Busted.** After a hand, a player with stack 0 is `Busted` and is not dealt in until chips are added.
- **Stand / disconnect during a hand.** `Stand` folds a live hand at once, even out of turn. If that player was to act, play moves on. If one live player is left, that player wins.
- **Timeout.** `DefaultAction` = Check when legal, else Fold.
- **Chip conservation.** Sum of stacks + all bets + pots never changes within a hand, except through Sit/Stand/AddChips. Test this as an invariant.

### Event text (log lines)

Short, present tense, one line each. Examples:
`Hand #12 — Alice has the button`, `Bob posts small blind 10`, `Carol posts big blind 20`,
`Flop: K♠ 7♥ 2♦`, `Turn: K♠ 7♥ 2♦ [9♣]`, `Alice raises to 60`, `Bob calls 50`, `Carol folds`,
`Bob checks`, `Alice bets 120`, `Bob is all-in for 340`, `Uncalled 80 returned to Alice`,
`Bob shows A♠ K♦ — Pair of Kings`, `Bob wins 400 from the main pot with Pair of Kings`,
`Alice wins 120 from side pot 1`, `Carol is busted`.

## Server rules

- **Identity.** `key` = SSH public key fingerprint (`SHA256:...`) when the client offers a key, else `user:<ssh-username>`. Display name = SSH username, cut to 12 runes, printable only. If another online key uses the name, `Connect` returns `ErrNameInUse`.
- **Bank and leaderboard.** Each key has a ledger `{Name, Bank, BuyIns int, BoughtChips int64}`. `Sit` moves the whole bank to the table; when the bank is 0 the player buys in: `BoughtChips += StartingStack`, `BuyIns++`. `Rebuy` does the same for a busted, seated player. `Stand` moves the stack back to the bank. `Net = Bank + stack - BoughtChips`. The room saves the ledger to `StatePath` (atomic write: temp file + rename) after each hand and each seat change, and on shutdown (batch 7 hardening: a failed save goes to the host log, and `SaveErr` reports it). On restart, chips that were on the table are in the bank (save the stack into the bank view when saving).
- **Hand loop.** When no hand runs and `CanStartHand()`, the room waits `NextHandDelay` (show `NextHandAt`), then calls `StartHand`. Re-check before the start.
- **Turn clock.** When `TurnTimeout > 0`, the player to act has that long. Then the room applies `DefaultAction`. A disconnected player's turn acts at once.
- **Disconnect.** `Close` marks the seat disconnected. At hand end, a disconnected seated player stands up (chips to bank). A reconnect with the same key before that keeps the seat.
- **Snapshots.** Each session gets a personal snapshot (own hole cards only). Channel buffer 1, newest wins (drain then send, non-blocking). Send after every change, and each second while a clock or next-hand timer runs.
- **Chat.** Strip control characters and ANSI escapes. Max 200 runes. Keep the last 100 lines. Rate limit: 5 messages per 5 seconds per session.
- **Log.** Keep the last 200 engine event lines plus room lines ("Alice joined", "Bob left").
- **Concurrency.** Only the `Run` goroutine touches the engine, ledger and session map. Session methods send a command struct with a reply channel and wait (or return `ErrRoomClosed` when the room stopped). Must pass `go test -race`.

## TUI spec

Draw a `server.Snapshot` with lipgloss. Felt-green oval table feel, but text only.
Layout target: 100×30 and up; must stay usable at 80×24 (compact mode).

- **Table:** seats around the board. Each seat box: name, stack, street bet, last action, card backs `🂠`/`[##]` or face cards. Mark button `(D)`, `SB`, `BB`, the player to act (bright border), folded (dim), all-in, sitting out, busted, disconnected. The viewer's seat is at the bottom centre (rotate seat order so the viewer is at the bottom).
- **Cards:** draw as small boxes, e.g.
  ```
  ┌───┐
  │A♠ │
  └───┘
  ```
  Red for ♥ ♦, white/black for ♠ ♣. Compact mode: `A♠ K♥` inline.
- **Centre:** board cards, pot total and side pots, street name, hand number.
- **Action bar** (only on your turn): `[F]old  [C]heck/[C]all 40  [R]aise  [A]ll-in 980`. `R` (or `B` when bet) opens an amount input: type digits, or ←/→ step by one big blind, ↑/↓ step by pot fractions; presets `1` = ½ pot, `2` = ⅔ pot, `3` = pot, `4` = all-in (clamp to MinTo..MaxTo). Enter confirms, Esc cancels. Show the turn clock as a bar/countdown.
- **Not seated:** show `[S]it` and the spectator list. **Busted:** `[B]uy back in`. Seated: `[O] sit out / back in`, `[L]eave seat`.
- **Side panel / tabs:** chat (the first and default tab), log (last lines), players. `Tab` cycles the panel. `T` or `/` opens chat input; Enter sends; Esc cancels.
- **Hand result:** after a hand, highlight winners, show shown hands and the hand name for `NextHandDelay`.
- `?` toggles help. `Q` / `Ctrl+C` quits (ask to confirm when in a live hand).
- Show one-line error notices from Session calls (e.g. "it is not your turn") for 3 seconds.
- Never block the Bubble Tea update loop on a Session call: run each call in a `tea.Cmd`.

---

# Batch 2 (features 1–9)

The contracts above were extended for batch 2: `engine/api.go` (Config.FirstHand,
Result.Players/NoShowdown/CanRabbit/Rabbit/Revealed, PlayerSummary, Event.Street/Action,
EvRabbit, EvReveal, RabbitHunt, ShowCards, Rename, HandNumber), `server/api.go`
(errors, Config money/admins, Me fields, PlayerStats, Money, Balance, Transfer,
Settlement, Snapshot.Stats/Settle/Alert, ConnectWith, KnownKey, PINAccountExists,
CheckPIN, CreatePINAccount, SetName, LinkPIN, RabbitHunt, ShowCards, SetAlerts,
MarkSettled, ReadStats, MergeAccounts) and `tui/api.go` (Options, Alert, New).

## 1. Amount editor keys

Presets: `h` = ½ pot, `t` = ⅔ pot, `p` = pot, `a` = all-in. Digits `0`–`9` ALWAYS
type the amount (the first digit typed replaces the prefilled amount; later
digits append). Backspace deletes a digit. `←`/`→` = one big blind less/more,
`↑`/`↓` = next/previous pot-fraction step. Enter sends. Esc closes the editor.
`c` = check/call and `f` = fold work while the editor is open (they close it and
act). Remove every other hidden binding in the editor (no vim keys). Pot sizes
use the standard pot-raise formula: pot-size raise total = current bet + (pot
after calling). Clamp to MinTo..MaxTo; show "ALL-IN" when the amount equals MaxTo.

## 2. Your hand under your cards

When the viewer holds live cards and the board has 3+ cards, show the best hand
name directly under the viewer's hole cards (e.g. "Two Pair, Kings and Sevens"),
and mark the cards that make it (hole cards and board cards) with a highlight
(e.g. a bright/underlined edge). Preflop show "Pocket Kings" / "A♠ K♦ suited"
style hint is optional. Use `cards.Best`. Compact mode: show the name on the line
under the viewer's inline cards. Never show it for other players' hidden cards.

## 3. Stats dashboard

`D` opens a full-screen dashboard (Esc or `D` closes it). Two tabs, switched
with Tab or ←/→: **Stats** and **Settle up**.

Stats table columns (drop low-priority columns at narrow widths, in this order of
priority): Player, Net (period, chips and money), Hands, Won, VPIP %, PFR %,
WTSD % (showdowns / saw flop), W$SD % (showdowns won / showdowns), Biggest pot,
Best hand, All-ins, Buy-ins, Lifetime net. Mark online players (●) and the viewer.
Percentages show "—" when the denominator is 0. ↑/↓ moves a row cursor; Enter on
a row shows a detail card for that player (best hand with its 5 cards drawn,
all numbers with labels and one-line explanations of VPIP/PFR/WTSD/W$SD).

Stats come from `engine.Result.Players` at each hand end (server). Hands = dealt
in. HandsWon = Won > 0. BiggestPot = max Won in one hand. BestHand = max
`HandValue` shown at showdown (compare with `HandValue.Compare`), store value and
cards. Persist all stats in the state file.

`stack-overflow stats` (host CLI) prints the same table plus the settlement to
stdout (works while the server runs: it reads the file without the lock, read-only).

## 4. Settle up

Money: `ChipsPerUnit` chips = 1 currency unit (default 5, so a 1,000-chip buy-in
costs ₹200). Defaults: stack 1000, blinds 5/10, currency "₹", next-hand delay 20s.
The state file keeps the rate of the open period (batch 7 hardening, money-1).
Flags: `-chips-per-unit`, `-currency`, `-admin name1,name2` (batch 7: credentials, not names).

Period net = bank + chips on table (incl. chips in a live hand) − chips bought in
this period. Settle tab: balances (green = receives, red = pays), then the
transfers: "Bob pays Alice ₹350.00" (format money with the currency symbol and
Indian/plain thousands separators; drop ".00" when whole). Then history of past
periods. Admins see "[M] Mark everything settled" which asks to confirm (type
`yes`). Admin = connection from a loopback address (host machine) or a name
in `-admin` (batch 7: a credential in `-admin`). Only between hands; it stands everyone up, zeroes banks and period
counters, keeps lifetime stats and records the period in History.

Greedy transfers: repeatedly match the largest debtor with the largest
creditor, pay min(|debt|, credit). Money rounding: convert chips to hundredths
with round-half-away-from-zero, then give any ±1 remainder to the largest
balance so the money balances sum to exactly zero.

## 5. Rabbit hunt

Between hands, when `Result.CanRabbit`, any seated player can press `H`. The
engine deals the remaining board cards from the same deck (burn before each
street as in a real deal) into `Result.Rabbit`. The TUI draws them next to the
board, dimmed/dashed and labelled "rabbit". Log line: "Rabbit hunt (Bob): [9♣] [A♥]".
The deck of the last hand must therefore be kept until the next StartHand.

## 6. Winner shows

Between hands after a NoShowdown win, the winner sees "Show: [1] left  [2] right
[3] both". The engine adds the cards to `Result.Revealed`; all viewers see them
face up on the winner's seat with a "shown" mark; log "Alice shows A♠". A second
call can add the other card. Never possible after a showdown or for non-winners.

## 7. Alerts (bell + desktop notification)

Server sets `Snapshot.Alert` = "turn" in the first snapshot where the viewer must
act, and "hurry" once when ≤5s remain on the viewer's clock. Each cue appears in
exactly one snapshot for that session. The TUI, when `Me.Alerts` is on, calls
`Options.Alert`: on "turn": Bell + Notify("Your turn — <last action text, e.g.
Bob raises to 60>"); on "hurry": Bell + Notify("5 seconds left"). Notify only when
the terminal reported focus-out (Bubble Tea focus reporting: `tea.WithReportFocus`;
handle `tea.FocusMsg` / `tea.BlurMsg`; assume focused until the first BlurMsg). Bell always.
`N` toggles alerts (SetAlerts, saved per account) and shows a notice.
cmd writes BEL as "\a" and the notification as OSC 9: "\x1b]9;<title>: <body>\x07"
(strip control chars from the text), through a mutex-protected writer that also
wraps the program output (tea.WithOutput), so an alert is never written inside a frame.

## 8. Name: Stack Overflow

App name everywhere: "Stack Overflow" (title bar, welcome, help, README, host
startup banner, SSH connect errors). Binary `stack-overflow` built from
`cmd/stack-overflow`. State dir `~/.stack-overflow/` (`state.json`,
`host_ed25519`). The old files in `~/.office-poker/` come over with two rules
(see the batch 10 audit). The host key comes over when the new host key does not
exist, so colleagues get no host-key warning. The old bank comes over only one
time: when the new folder has no host key, no `state.json` and no marker file.
Then marker files in both folders stop a second copy. One line tells about each
copy. State file v2 migrates v1
ledgers (each v1 key becomes one account; "user:<name>" v1 identities become PIN
accounts WITHOUT a PIN: the first login with that name sets the PIN).

## 9. Player profiles

Identity: `key:<SHA256 fp>` for key logins; `pin:<login>` for keyboard-interactive
logins (login = lower-cased SSH user, trimmed, 1–32 runes). Accounts have an
internal id, a display name, any number of key fingerprints and at most one
PIN login (bcrypt hash, golang.org/x/crypto/bcrypt, cost 10).

SSH auth (cmd):
- Public key offered → accept (record the fingerprint in the ssh context
  permissions/extension as now). If `-password` is set, ALSO require the
  shared password via keyboard-interactive (as now).
- No key → keyboard-interactive: (if `-password`: ask the shared password
  first), then if `PINAccountExists(login)`: prompt `PIN for "<login>": ` (echo
  off) → CheckPIN; else prompt `New player "<login>". Choose a 4-digit PIN: ` and
  `Type the PIN again: ` → CreatePINAccount. Up to 3 tries per connection.
  Also allow the plain "password" auth method for the PIN (some clients prefer
  it) only when an account exists.
- Shared password: also read from env `STACK_OVERFLOW_PASSWORD` (flag wins) so it
  is not visible in `ps`.
- Limits: at most 64 concurrent SSH connections in total and 8 per remote IP;
  extra connections get a short message and close.
- Admin: remote address is loopback, or display name in `-admin` (batch 7:
  a credential in `-admin`).

Welcome screen (TUI) when `Me.NeedsName`: box titled STACK OVERFLOW, "Pick the
name others see", input prefilled with `Me.Name`, Enter saves (SetName), Esc keeps
the suggested name (SetName with it). When `Me.CanLink`, also "Played before with
a PIN? Press Tab (or Ctrl+L) to link" → a two-field form (login, PIN, masked) → LinkPIN.
When `Me.Keyless`, show the tip: "Make an SSH key and never type a PIN again:
ssh-keygen -t ed25519 (press Enter 3 times)".
`M` (my profile) opens a small box: rename (SetName), alerts on/off, and shows
the login type (key / PIN).

Names: 1–12 runes after trimming; letters, digits, space, `-`, `_`, `.`; unique
across ALL accounts (NFKC + case fold; use golang.org/x/text). Reject confusable
mixes simply by allowing only one script per name (Latin, digits and the
punctuation above are always fine).

`stack-overflow merge "<from>" "<to>"` merges two accounts (server must be
stopped; ErrLocked otherwise).

Hand numbers continue across restarts (state file keeps the counter; pass
Config.FirstHand to the engine).

---

# Batch 3: more stats

Contracts extended: `engine.Result.Actions/Order/BigBlind/RunoutFrom` and
`engine.ActionRecord`; `cards.Equity`; `server.PlayerStats` fun fields and
`Host *HostStats`; `server.HostStats`; `ConnectOptions.Host`; `Me.Host`.
Stats count only hands from batch 3 onward (old hands have no action log).
State file: bump the version and migrate (new fields start at zero).

## Visibility

- **Fun stats** (everyone): bad beats, luckiest river, first win, nemesis.
- **Favourite hand**: only in the viewer's OWN row (`FavouriteHand` empty in
  every other row, for the host too). Never derive anything visible to others
  from hole cards that were not shown.
- **Host stats** (`PlayerStats.Host`): only when `Me.Host`. Host = connection from
  the server machine: loopback, or a remote IP equal to one of the machine's own
  interface addresses (cmd decides; re-read interfaces at each connect).
  A host is also an admin. `stack-overflow stats` prints everything (the file
  is the host's).

## Fun stat definitions

- **Bad beat**: a hand with a run-out (`RunoutFrom < 5`) where the player was
  live at the run-out with `cards.Equity` >= 0.70 for the MAIN pot (equity among
  the players eligible for the main pot, from the board at `RunoutFrom`) and
  won less than a full share of the main pot (lost it or split it). Count
  `BadBeats`; keep the highest equity in `WorstBeat` with the player's hole
  cards in `WorstBeatWith`. Use exact enumeration for <= 2 cards to come and
  20,000 samples otherwise.
- **Luckiest river**: a showdown hand where the main-pot winner (any winner if
  split) had a strictly worse best hand than some other main-pot contender on
  the turn (4-card board + hole cards) and won on the river. Amount = the
  player's Won in that hand. Keep the max in `LuckiestRiver` with `RiverCard`.
- **First win**: `FirstWinAfter` = the player's hand count (dealt in) at the
  first hand with Won > 0. Fixed once set.
- **Nemesis**: per hand, for each loser L (Won-Committed < 0) and winner W
  (> 0): flow(L→W) += |netL| × netW / (sum of positive nets), rounded to the
  nearest chip. Keep a pairwise matrix by account id (persist; MergeAccounts
  merges rows/columns; MarkSettled keeps it). Nemesis of X = the Y with the
  largest flow(X→Y) − flow(Y→X), when > 0.
- **Favourite hand**: count canonical starting hands ("AKs", "AKo", "77";
  higher rank first) for hands where the player had VPIP. Show the most
  frequent (tie: the stronger by rank order) with its count.

## Host stat definitions (all from `Result.Actions` and `Result.Order`)

Preflop, the big blind is not a raise (`RaisesBefore` counts bets/raises only).
- **3-Bet**: chance = the player acts preflop facing exactly one raise
  (`RaisesBefore == 1`) and did not make that raise; 3-bet = the action has
  `Raise`. Count each chance at most once per hand (the first such decision).
- **Fold to 3-Bet**: chance = the player made the first preflop raise and later
  faced a re-raise (their next preflop decision has `RaisesBefore >= 2`); fold =
  that decision is Fold.
- **Steal**: chance = the player's first preflop decision has `RaisesBefore == 0`,
  no player before them called (everyone before folded), and the player is the
  cutoff, the button or the small blind (positions from `Order`; cutoff = the
  seat before the button, only with 4+ players dealt in). Steal = that decision
  raised.
- **Limp**: chance = the player's first preflop decision has `RaisesBefore == 0`
  and the player is not the big blind; limp = that decision is Call.
- **C-Bet**: chance = the player made the last preflop raise, is live on the
  flop, and every flop decision before theirs was a Check (or they act first);
  c-bet = their first flop decision has `Raise` (a bet).
- **Fold to C-Bet**: chance = the player's first flop decision after a
  c-bet (as defined above, by another player) when no other raise came between;
  fold = that decision is Fold.
- **Check-raise**: chance per postflop street = the player checked and a later
  decision on the same street by another player bet, and the player acts again
  on that street; check-raise = that next decision has `Raise`.
- **AF**: postflop bets + raises (Raise true) vs postflop calls (Call, and an
  AllIn that is not a raise). Show `bets/calls` with one decimal; "∞" when calls
  are 0 and bets > 0; "—" when both 0.
- **bb/100** = 100 × NetBB / HostStats.Hands (one decimal). Both count only from batch 3 onward.
- **Win rate / hour** = period-agnostic net money since batch 3 (`HostStats.NetChips`) per hour of
  `SeatedSeconds` (count only while seated and not sitting out); "—" under 10 minutes.
- **Biggest loss**, **Longest win streak** (consecutive hands dealt in with
  Won > 0; reset by a hand with Won == 0), **All-in win %** = AllInShowdownsWon /
  AllInShowdowns (AllIn and WentToShowdown).

## TUI

Dashboard tabs: **Stats** | **Fun** | **Settle up** | **Host** (Host tab only
when `Me.Host`). Fun columns: Player, Bad beats (worst %), Luckiest river (amount
and card), First win, Nemesis (name and chips), and "Your favourite hand" in a
line under the table for the viewer only. Host tab columns, by priority for
narrow screens: Player, bb/100, ₹/hour, VPIP/PFR (from Stats), 3-Bet, AF,
C-Bet, Fold to C-Bet, Fold to 3-Bet, Steal, Limp, Check-raise, Biggest loss,
Win streak, All-in win %. Each % shows "—" with 0 chances; the detail card
(Enter) shows "count/chances" and one-line explanations. The `stats` CLI prints
the Fun table and the Host table too.

## Batch 4: time extensions

Each player gets `Config.Extensions` time extensions (default 3) for one
server run. One extension adds `Config.ExtensionTime` (default 30 s) to the
player's turn clock.

Rules:

- The count belongs to the account, not to the SSH connection. A reconnect
  does not give new extensions. A server restart gives every player the full
  count again. The Room keeps the count in memory only (not in state.json).
- A player can extend only on their own turn while the turn clock runs.
  Otherwise Extend returns ErrCannotExtend. With no extensions left, Extend
  returns ErrNoExtensions. Extensions == 0 disables the feature: Extend
  returns ErrNoExtensions and Me.ExtensionsLeft is 0.
- A player can use more than one extension in the same turn. Each use adds
  ExtensionTime to the current deadline (not to "now").
- Snapshot.ClockTotal is TurnTimeout plus ExtensionTime for each extension
  used in the current turn. It resets at each new turn.
- After an extension, the "hurry" alert can fire again when 5 s or less
  remain on the new deadline (one "hurry" for each deadline, not for each turn).
- The log shows "<name> adds 30s (2 left)" (the seconds come from
  ExtensionTime).
- The room sends a new snapshot to every session after an extension.
- A timeout after an extension works as before: the default action, then
  the player sits out.

TUI:

- `E` uses one extension. It works on the action bar of the player's own
  turn and in the amount editor. It does not work in chat input or on the
  welcome screen, where `e` is a typed letter.
- The action bar shows `E +30s (3)` while the player has extensions and the
  clock runs. With none left, the hint is not shown. The hint and the help
  write ExtensionTime in the same form as the log ("30s", "1.5s"), and do
  not round it.
- The timer bar on the acting seat measures the time left against
  Snapshot.ClockTotal (fall back to Config.TurnTimeout when it is zero). So
  after an extension the bar becomes full again and then shrinks. When the
  seconds label leaves no cell for the bar (10000 s or more in a narrow
  space), the timer bar is not shown.
- An error from Extend shows on the status line, as other errors do.
- The help screen (`?`) and the README key list name `E`.

cmd: the flags `-extensions` (default 3) and `-extension-time` (default 30s).

## Batch 5: buy-in picker

When a player sits down, the player picks the amount to bring.

Server (contract in internal/server/api.go: SitChoice, SitWholeBank,
SitBuyIn, Session.SitWith):

- SitWholeBank: the same as the old Sit. The whole bank goes to the table.
  With a bank of 0, the player buys in for StartingStack.
- SitBuyIn: the player sits with exactly StartingStack.
  - Bank >= StartingStack: StartingStack moves from the bank to the table.
    The rest stays in the bank.
  - 0 < bank < StartingStack: the whole bank moves to the table and the
    player buys the difference (a top-up). BoughtChips += difference and
    BuyIns++ (a top-up counts as one buy-in).
  - Bank 0: a normal buy-in (BoughtChips += StartingStack, BuyIns++).
- Rebuy (busted, seated player) uses the same rule as SitBuyIn: bank first,
  then a purchase for the rest. BuyIns++ only when the player buys chips.
- Net, settle-up and the leaderboard do not change: they already count
  bank + table - BoughtChips.
- Me.Bank shows the chips left in the bank while the player is seated.
- Stand moves the stack back into the bank and adds it to the chips that
  are already there.

TUI:

- `S` with a bank of 0 sits at once with a buy-in, as before.
- `S` with a bank > 0 opens a small picker over the table:

  ```
  Sit down with
    1  Buy-in       1,000      (or: 300 from bank + 700 top-up ₹350)
    2  Whole bank   2,450
  1/2 pick · Esc cancel
  ```

  `1` and `2` sit at once. ↑/↓ moves the highlight and Enter picks it. Esc
  closes the picker. Only these two options exist (no typed amount). The
  top-up text shows the chips to buy and their money value (ChipsPerUnit,
  Currency). With a bank >= StartingStack, option 1 shows
  "1,000 · 1,450 stays in bank".
- The picker closes when a new snapshot gives the viewer a seat. It also
  closes when the bank becomes 0 while it is open (for example, the host
  marks the period settled). The notice line then says "Your bank is now
  empty. Press S to buy in." While a SitWith call runs, the picker stays
  open until the result arrives.
- The "You are watching" line no longer says "you bring your bank of N".
  It says "S Sit down (bank N)".
- The busted "Buy back" hint says "from bank" when the bank covers the
  rebuy, and shows the top-up when it covers only a part.
- The picker fits at 80x24. The help screen and README describe it.

## Batch 6: one-minute clock and a slow run-out

### Turn clock

The default of `-timeout` changes from 30s to 60s. Extensions stay 30s.

### Run-out

When betting ends with two or more live players and fewer than 5 board
cards (players are all-in), the board no longer comes out at once.

Engine (contract: Config.StepRunout, View.Runout, Table.RunoutPending,
Table.DealRunout):

- With StepRunout true, the engine stops at the end of betting. It returns
  the uncalled bet first (as before), sets runoutFrom, and keeps the hand
  live: InHand true, ToAct false, View.Runout true. It deals no card.
- At that moment every live player's hole cards become face up for every
  viewer, and the engine emits one EvShow line for each live player, in
  showdown order ("Alice shows A♠ A♦"). The showdown at the end does not
  print these lines again.
- Each DealRunout burns one card and deals the next street (3 cards for the
  flop, then 1, then 1) and emits EvStreet as before. After the river, it
  runs the normal finish (showdown, pots, Result). Deck order and burns are
  the same as with StepRunout false, so the same deck gives the same board.
- When the last betting ends on the river, nothing changes (no run-out).
- A player who leaves (Stand) during a paused run-out: the same rule as an
  out-of-turn fold today. When only one live player is left, the hand ends
  at once and that player wins. Chips must be conserved.
- When a stand ends a paused run-out, the hand of the winner was face up
  since the run-out start. Result.Revealed holds both cards of the winner,
  and ShowCards returns ErrNotNow for that hand. So the TUI shows no
  "Show your cards?" prompt, and the log gets no second show line.
- With StepRunout false, the behaviour and every existing test stay the same.

Server:

- Config.RunoutDelay (flag `-runout-delay`, default 3s). 0 sets
  StepRunout false.
- When the table has a pending run-out, the room runs no turn clock and
  schedules the next street at now + RunoutDelay. The tick deals it with
  DealRunout, logs the events, and broadcasts. This repeats until the hand
  ends, and then the normal end-of-hand path runs (stats, save, next hand).
- The tick must run while a run-out timer is set (today it returns early
  when no deadline and no next-hand time exist).
- A server stop during a run-out works as a stop during any live hand.
- Stats (bad beat, equity, RunoutFrom) do not change.

TUI:

- While View.Runout is true, the board header says "ALL-IN · RUN-OUT"
  instead of the street name, and every live seat shows its face-up cards.
- The action bar shows no actions, and the status line says
  "All-in. Dealing the board…".
- The rest of the screen works as before.

## Batch 7: admin by credential, the panic key, and hardening

### Admin by credential

- `-admin` takes credentials, not display names. Each comma-separated entry
  is `SHA256:<fingerprint>` or `pin:<login>`.
- The room matches the credential that the session logged in with:
  - `SHA256:` matches `Session.fp` exactly.
  - `pin:` matches the PIN login of a keyless session, but only through
    the account id (B7-2). At `NewRoom`, after `pruneIdle`,
    `adminSet.resolvePINs` finds the account that holds the login with a
    PIN hash (`byLogin`, as a PIN login finds it). `isAdmin` compares the
    session key with these ids. A `pin:` entry without such an account
    makes nobody an admin until the next start: else the first person to
    create that PIN login (any keyless SSH client can) would become an
    admin. A legacy profile with the login and no PIN does not count.
    `Room.IgnoredAdmins` (api.go) lists these entries. The host log warns
    for each one, and the banner shows `pin:<login> (ignored)` and a
    warning. `AccountInfo.HasPIN` lets `accounts` list `pin:<login>` only
    for an account with a PIN.
- A display name never makes a player an admin, because any player can
  change a name.
- A connection from the server machine is still always host and admin.
- `NewRoom` rejects any other entry with an error that wraps `ErrBadAdmin`.
  `main` prints that error and exits with code 64 (`exitUsage`).
- The new host command `stack-overflow accounts [-state path]` prints each
  account with its name, its PIN login and its key fingerprints. The host
  copies a value from it into `-admin`. `ListAccounts` returns `ErrLocked`
  while a server runs. The command then tells the host to stop the server,
  or to look up the fingerprint with `ssh-keygen -lf <key.pub>`.
- The startup banner lists the admins by credential. A key admin shows as
  the account name plus a short fingerprint, when the account exists.
- After `LinkPIN`, a key session belongs to the PIN account, but it logged
  in with the key. Admin follows the credential of the login: only a
  `SHA256:` entry for that key makes the session an admin. A `pin:` entry
  for the account matches only a PIN login. The host lists the key too when
  the player must be an admin from a key login. The legacy `user:` identity
  of the old `Connect` never matches a `pin:` entry.
- `ParseAdmin` (api.go) is the one check of an entry. `-admin` uses it
  when it parses the flag, so a bad entry stops the server before it
  starts (exit code 64). A `SHA256:` entry has exactly 43 base64 characters
  after the prefix, as `ssh-keygen -lf` prints them. The prefix is not
  case-sensitive.
- `ListAccounts` takes the state lock, as `MergeAccounts` does. (`ReadStats`
  takes no lock.) A running server keeps newer accounts in memory than the
  file has, so the list could be wrong. `main` calls `ListAccounts` once
  before `NewRoom` to name the admins in the banner.

### Panic key

- **Keys: F20 and Ctrl+H (B7-3).** Cmd+H never reaches an SSH program:
  macOS hides the terminal window first. So the README maps Cmd+H to the
  F20 escape sequence `ESC [34~`, which Bubble Tea reports as "f20". No
  common keyboard has F20.
  - Ghostty: `keybind = cmd+h=text:\x1b[34~`
  - iTerm2: Key Mappings > Cmd+H > Send Escape Sequence `[34~`
  - kitty: `map cmd+h send_text all \x1b[34~`
  - WezTerm: `SendString '\x1b[34~'`
- F20 is the panic key on every screen, also in a text input.
- Ctrl+U (NAK, 0x15; Bubble Tea "ctrl+u") is the panic key only when no
  text input has focus, the same rule as Ctrl+H. Ghostty sends 0x15 for
  Cmd+Delete by default (`super+backspace=text:\x15`), so Cmd+Delete
  hides the game with no setup. In a text input, Ctrl+U keeps its usual
  job and clears the line.
- Ctrl+H (BS, 0x08; Bubble Tea "ctrl+h") is the panic key only when no
  text input has focus (`model.textFocus`: chat, raise amount, welcome,
  link, rename and the settle-up `yes` field). In a text input, 0x08 is
  Backspace. Many terminals send 0x08 for Ctrl+Backspace (kitty and
  Ghostty in legacy mode, VTE, Windows Terminal), and some send it for
  Backspace. A word delete in the chat must not hide every screen in the
  room. Backspace on macOS terminals sends DEL (0x7f), which Bubble Tea
  reports as "backspace".
- `Session.Panic()`:
  - It adds 1 to the room's panic counter, which every snapshot carries as
    `Snapshot.PanicSeq`.
  - It sends a fresh snapshot to every session.
  - Per-account cooldown: `PanicCooldown` (15 s), else `ErrPanicTooSoon`.
    The TUI shows that error in the status line only when the player is not
    already on the panic screen.
  - The server log (stderr) gets one line with the caller's name. The table
    log and the chat get nothing, so the panic stays anonymous. The server
    package writes the line with the default `charmbracelet/log` logger,
    the same logger as `main`.
- **TUI: panic screen.** It starts when a snapshot carries a `PanicSeq`
  higher than the last one that the model saw. The first snapshot of a
  connection is the baseline, so a player who joins later sees nothing.
  - **Phase 1 (1 s):** a full-screen notice in the middle of the screen:
    `🤫  Shh! Someone is coming!`. It uses a tea.Tick of 1 s.
  - **Phase 2: decoy.** The whole screen looks like a dull developer
    terminal. It is a plain shell prompt that runs `go test -v ./...` on an
    invented `inventory-service` module, with RUN/PASS lines and package
    timing lines. A new line comes about every 700 ms. The decoy loops the
    script when it ends. It uses only default colours, and it has no poker
    word, no card, no chip count and no player name.
  - When it is the viewer's turn, the decoy shows one quiet line at the
    bottom, with the seconds left on the clock:
    `--- WAIT  TestCheckoutFlow (45s)`. This line is the only hint.
  - While the panic screen is up, the model sends no desktop notification
    (OSC 9) and rings no bell, because a popup gives the game away.
  - **Exit:** any key except a panic key (Ctrl+H or F20) ends the panic
    screen and shows the table again. That key does nothing else, so a
    stray "f" does not fold. A panic key on the panic screen does nothing. A mouse event or a focus event
    does not end it. A window resize redraws it.
  - A new panic during phase 2 does not restart phase 1.
  - While the panic screen is up, the window title is
    `inventory-service — zsh`. The exit sets the table title again.
  - Each phase tick carries a generation number. The exit changes it, so a
    tick from an old panic does nothing.
- The game continues during a panic: the clock runs and the clock acts for
  a player who does not act.

### Hardening

The batch 7 audit lists the backend, build and reliability fixes. Each fix
has a test or a reason in its commit message. A fix keeps every contract
in api.go, except that a fix can add to a contract.

- **SEC-1, weak PINs and silent guesses.** `CreatePINAccount` refuses the
  PINs that people choose most with `ErrWeakPIN`: four equal digits, a run
  up or down, a repeated pair (ABAB), `2580`, `0852`, `1004` and the years
  `1900`-`2099`. `WeakPIN` (api.go) is the same check; the SSH dialog uses
  it before it asks for the PIN again. A PIN that exists stays valid. Each
  wrong PIN (`CheckPINFrom` and `LinkPIN`) writes a host log line with the
  address and a table log line (`Wrong PIN for <name> (<n> in the last
  hour)`). A `LinkPIN` that adds a key writes a host log line with the key
  and the address, and the table line `A new SSH key now logs in as
  <name>`. The new host command `stack-overflow unlink SHA256:...`
  (`UnlinkKey`) removes a key. It refuses to remove the only way to log in.
  `accounts` already lists the keys.
- **SEC-2, a loop of new keys.** `uniqueName` builds the set of taken names
  once, so one connect costs O(accounts), not O(accounts²). `byKey` and
  `byLogin` do not sort the ids. An *idle* account (a key account that is
  `fresh`, not `Named`, with no PIN and no settings) is not saved: a new
  connect does not save it, `diskView` leaves it out, and `NewRoom` removes
  such accounts from an older file. The welcome screen calls `SetName` on
  Enter and on Esc, so a real player is never idle after the first screen.
  One remote address (`ConnectOptions.Addr`) can make at most 20 new key
  accounts per hour (`ErrTooManyKeys`). The host and an empty address have
  no limit.
- **SEC-3, lock-out by wrong PINs.** The short lock counts per login *and*
  remote address (host part): 5 wrong PINs in 10 minutes lock that pair for
  10 minutes. A second guard per login counts all addresses: 20 wrong PINs
  in 1 hour lock the login for 1 hour. So a prankster cannot keep the owner
  out from another computer, and a guesser on many computers still hits a
  limit. `CheckPINFrom` (api.go) takes the address; `CheckPIN` is the same
  with an empty address. Each lock writes a host log line with the address.
- **SEC-4, look-alike names.** `nameTaken` and `uniqueName` compare
  `skeleton(name)`: `nameKey`, then a small map from Cyrillic and Greek
  letters (and `ı`, `ɑ`, `ɡ`) to the Latin letters that they look like.
  Stored names do not change. Digits that look like letters are not in the
  map.
- **REL-1, idle sleep.** On macOS, `run` starts `caffeinate -i -s -w <pid>`
  and logs a warning when it cannot. `scripts/serve.sh` also runs
  `caffeinate -ims` while it runs.
- **REL-2 and OPS-3, silent save failures.** `save` gives each result to
  `noteSave`. The first failure, and each new failure text, goes to the
  host log (stderr) and the table log; the first save that works after a
  failure says so. `Room.SaveErr` (api.go) returns the result of the last
  save. After `Run` returns, `main` prints `room stopped; state saved` only
  when the final save worked. Else it prints `state NOT saved` and exits
  with code 1.
- **REL-4 and OPS-1, SIGHUP and crashes.** `run` stops on SIGHUP as on
  Ctrl+C and SIGTERM. The server ignores SIGPIPE, so a closed log pipe
  cannot kill it before the save. A panic in a room command does not save:
  the state in memory can be half-changed, so the last save stays on disk.
  `Run` then releases the lock and the callers (`ErrRoomClosed`) and panics
  again. `scripts/serve.sh` starts the server again after a crash (2 s
  pause; it stops after 5 fast crashes in a row, on exit 0 and on exit 64),
  writes `~/.stack-overflow/server.log` with a time on each line, and sends
  exactly one SIGINT to the server for the first Ctrl+C, SIGTERM or SIGHUP.
  A later SIGHUP sends nothing. A second Ctrl+C or SIGTERM sends a second
  SIGINT, and that SIGINT stops the server at once (the forced stop).
- **B7-1, exit codes.** The Go runtime exits with code 2 after an
  unrecovered panic or a fatal error, and the re-panic of `Run` is such a
  crash. So bad flags, a bad `-admin` entry and bad host command arguments
  exit with 64 (`exitUsage`, EX_USAGE of sysexits.h), not 2.
  `scripts/serve.sh` stops only on 0 and 64, and starts the server again
  after 2 like after any other crash.
- **OPS-2, backups.** `backupState` copies the state file to
  `state.json.<UTC time>.<why>.bak` through `writeAtomic` and keeps the
  newest 10. It runs at each start (a failure is a host log warning),
  before a settle (a failure refuses the settle), and before `merge` and
  `unlink` (a failure stops the command). The `.v<N>.bak` migration copies
  do not count. The README has backup and restore steps.
- **OPS-4, flags.** `parseServerFlags` checks seats 2-10, sb > 0, bb >= sb,
  stack >= bb > 0, port 1-65535, chips-per-unit >= 1 and an IP address or
  `localhost` for `-host`, before any file work (exit code 64). `run` binds
  the port first and prints the banner only after the bind.
- **Build and version (with OPS-1).** The banner and `-version` show
  `main.version` (set by `make` with `-ldflags`), else the VCS revision
  from `debug.ReadBuildInfo`. `make build` writes `bin/stack-overflow.new`;
  `make install` renames it over `bin/stack-overflow` in one step.
- **OPS-6.** The README asks for Go 1.27.4, the `go` line of `go.mod`.
- **money-1, the money rate on restart.** The state keeps `chips_per_unit`
  and `currency` of the open period. At start, a period with chips, buy-ins
  or hands keeps its saved rate when the flags differ, and the host log and
  the banner say so. Else the period takes the flags. A settle records the
  rate in `PastPeriod.ChipsPerUnit`, and the next period takes the flags.
  `ReadStats` uses the saved rate. `Room.Config` (api.go) returns the rate
  in use at start.

## Batch 8: start key, hand rankings, card stability

### Start key (G)

- `Config.ManualStart` makes the room wait for a start. `main` turns it on
  by default. The new flag `-autostart` turns it off and gives the old
  behaviour.
- A room starts with `Snapshot.Waiting` true. It deals no hand while it
  waits, even with 9 players seated.
- Any seated player presses **G** to call `Session.Start()`:
  - Not seated: `ErrStartNotSeated`.
  - Fewer than 2 players can play (seated, not sitting out, chips > 0):
    `ErrStartNeedsTwo`.
  - Otherwise the first hand starts after min(NextHandDelay, 3 s). The
    table log gets one line, `<name> started the game`.
  - `StartedBy` names that player until the first hand is dealt.
- After a start, hands follow one another as before.
- When a hand is due but fewer than 2 players can play, the room goes back
  to `Waiting`. A new start is then necessary, so players can regroup
  after a break.
- TUI:
  - While `Waiting`, the status line says
    `Waiting to start · N seated · press G when everyone is in`.
  - A spectator who is not seated sees `Sit down with S. A seated player starts with G.`
  - The footer and the help list show G only when it is useful.
  - The error text of `ErrStartNotSeated` and `ErrStartNeedsTwo` shows in
    the status line.

### Hand rankings box

- **Full mode:** the right panel (Chat/Log/Players) becomes shorter. A
  "HAND RANKINGS" box fills the bottom right, under the panel, at the panel
  width. The panel keeps at least 8 lines. If there is no room for both,
  the box drops the examples first, and then it hides.
- **Compact mode:** there is no box. The existing best-hand hint under the
  viewer's cards stays as it is.
- The box lists the 10 poker hands from best to worst, each with a short
  example. Use rank letters only (T = ten), with no suit symbols:

  | # | Hand | Example |
  |---|---|---|
  | 1 | Royal flush | A K Q J T, one suit |
  | 2 | Straight flush | 9 8 7 6 5, one suit |
  | 3 | Four of a kind | Q Q Q Q |
  | 4 | Full house | K K K 7 7 |
  | 5 | Flush | any 5, one suit |
  | 6 | Straight | 8 7 6 5 4 |
  | 7 | Three of a kind | 8 8 8 |
  | 8 | Two pair | J J 4 4 |
  | 9 | Pair | A A |
  | 10 | High card | none of the above |

- When the viewer has hole cards in a live hand, the box marks the row of
  the viewer's best hand now (hole cards plus the board; preflop it is
  Pair or High card). It uses the same evaluator as the best-hand hint, so
  the two always agree. A line at the top reads `You: Two Pair`.
- The box never uses any other player's cards.
- The key **I** shows or hides the box for this session. The default is
  shown.

### Card stability

Two players reported that their cards "changed" during a hand. The server
log shows no crash or restart at that time. The rules below are
invariants. Tests check them, and the investigation records its result
here.

- During one hand (one `HandNumber`), each viewer's own hole cards never
  change after the deal. The board cards only grow: each snapshot's board
  starts with the board of the snapshot before.
- The cards that a seat shows always belong to that seat's player, also
  after a stand, a sit, a reconnect, a rename, a LinkPIN, a run-out, a
  show or a rabbit hunt.
- After a hand ends, the old hand stays on the screen until the next deal.
  Rabbit-hunt cards look different from dealt cards, and a log line names
  them. The next deal is clear: the board empties and the log says
  `── Hand #N ──`.

#### Result of the investigation

What caused the report:

- No card changed inside a hand. The server and engine path is ruled out.
  `TestCardStability` (server) drives random hands and events (sit, stand,
  sit out, reconnect, second connect, rename, LinkPIN, run-out, show,
  rabbit hunt, panic, rebuy, G) with ManualStart off and on. It checks every
  snapshot against the invariants above. Mutations that rewrite the board
  or leak rabbit cards make it fail.
- The TUI drawing path is ruled out. `TestDrawnCardsMatchSnapshot` and the
  run-out and result tests draw a 9-seat table from every seat and as a
  spectator, in full and compact mode. Each seat box draws only the cards
  of its own SeatView, and the board draws in order.
- The most likely cause is an unclear hand boundary. After a fold-around
  the board is empty, so the next deal changed only a small title and the
  two hole cards. The "next hand in" countdown was hidden when a rabbit
  line showed. Rabbit cards from other players filled the board. The hint
  mark changed the card edge during the hand. A player who drops for a
  short time comes back to a new hand.
- A second, possible cause on some clients (for example old Windows
  consoles): a glyph that the client draws wider than the server measures
  it makes a full-width line wrap. Old cells then stay on the next row.

What changed:

- `internal/server/cardstab_test.go`: the property test, with a
  `pushHook` in `Session.push` (nil in production).
- `internal/tui/cardstab_test.go`: drawn cards equal the snapshot cards at
  every seat rotation, in a run-out and after a hand; a sequence test of
  one hand and the next deal.
- The felt title of a result always holds the countdown:
  `RESULT · Hand #N · next hand in Ns`. The `H rabbit hunt` hint, the
  rabbit label and a split pot can no longer push the countdown out.
- The viewer's cards show `Hand #N` during a hand. During the result they
  are dim and say `last hand · #N`. A new hand number shows the notice
  `Hand #N · new cards` for 2 s, clears the screen and draws
  `── Hand #N ──` in the log.
- The card edge never changes. The hint underlines only the cards that make
  the hand category (a Pair marks 2 cards, no kickers), the same category
  as the hand rankings box. No starting-hand tiers.
- Rabbit cards keep a dashed edge in every form, and the felt says
  `rabbit · not dealt`. The log line names the hunter.
- The SSH server turns auto-wrap off (`ESC[?7l`) before the first frame
  and on (`ESC[?7h`) when the program ends.
- `clean` (chat and names) removes variation selectors, the keycap mark,
  the zero width joiner, skin tones, regional indicators and tag
  characters. It also drops a grapheme cluster whose grapheme width is not
  the sum of its rune widths. Indic vowel signs and accents stay.
- The room saves the hand number at each deal, so a restart after a crash
  never deals the same hand number again.

## Batch 9: a folded player can show at the end

- A player who folds can show one or both hole cards, but never during
  the hand. The cards show only after the hand ends.
- **After the hand** (the result pause), a player who folded and is still
  seated presses 1 (left card), 2 (right card) or 3 (both). This is the
  same key set as the NoShowdown winner's show. The engine puts the cards
  in `Result.Revealed` and logs `<name> shows <cards>`. The seat box shows
  the cards with the note `folded · shown`.
- **During the hand**, after the fold, the same keys store the choice.
  Nothing shows yet. The status line says
  `You will show A♠ K♦ when the hand ends · press 3 again to cancel`.
  When the hand ends, the room calls the engine show for each stored
  choice, in seat order, before the first snapshot of the result. The
  log then has the show lines right after the result lines.
- Same key again cancels. Another key changes the choice.
- The footer shows `1 2 3 Show` only when a show is legal: after the hand
  for the NoShowdown winner and for folded players whose cards are not
  all shown, or during the hand for a folded player.
- A stored choice is lost when the player stands or leaves the seat. A
  disconnect keeps it until the hand ends, because the seat stays until
  then.
- Players who reach showdown already show their cards. Nothing changes for
  them.
- Stats do not change. A show is not an action.
- While a show is legal for a folded player, that player's own seat draws
  the own hole cards face up but dim, with the word `folded` (or `Fold`
  after the hand). Only that player sees them: the engine gives the cards
  only to the owner's view, and the TUI draws them only in the viewer's
  own seat. So the player knows which key shows which card.
- The show prompt names the card of each key, for example
  `You folded. Show your cards?  1 A♠  2 K♦  3 both`. When one card is
  shown, the prompt names only the other card. The NoShowdown winner's
  prompt (`You win. Show your cards?`) names the cards too.
- The show prompt comes before the sit-out and the waiting lines of the
  status line, so that the footer hint `1 2 3 Show` always has a prompt.
  When it fits, a dim tail follows: ` · you are sitting out`,
  ` · waiting for players` or ` · waiting to start`. After all cards are
  shown, the old line comes back.
- A compact seat with a one-card show tries `folded · shown`, `folded`,
  then `shown`. When no mark fits, the seat draws only the cards (for
  example `10♦ ░░`). The card back already says that the player showed
  one card. The seat never draws a cut word such as `sho…`.
## Batch 10: rabbit hunt limit and "you would have had"

### Limit: 3 rabbit hunts for each player

- `Config.RabbitHunts int`: the number of rabbit hunts that each account
  can do in one server run. 0 means no limit (the old behaviour, so the
  old tests do not change). `main` sets 3 with the new flag
  `-rabbit-hunts` (default 3).
- The room counts per account, in memory only, like the time extensions.
  A reconnect keeps the count. A server restart resets it.
- `Session.RabbitHunt()` returns `ErrNoRabbits` ("no rabbit hunts left")
  when the caller has used all of them. The count goes up only when the
  hunt succeeds.
- `Me.RabbitsLeft int` is the number left (0 when Config.RabbitHunts is 0,
  and the TUI then shows no count).
- TUI: the hint reads `H rabbit hunt (2 left)`. With 0 left, the hint goes
  away. H then shows the error in the status line.
- The banner line: `3 rabbit hunts for each player (key H)`.

### "You would have had"

- After a rabbit hunt, each player who was dealt into that hand sees, only
  on their own screen, the best hand that their own 2 hole cards make
  with the full board: the dealt board plus the rabbit cards. The players
  who folded see it too.
- The server computes it per session, from the hole cards that the room
  keeps for each key (`Room.holes`), with the same evaluator as the
  best-hand hint (`cards.HandValue`):
  `Snapshot.Me.RabbitHand` (type `RabbitHand`: `Category`, `Hand` such as
  "Flush, Ace high", and `Best`, the 5 best cards). It is zero when
  there is no rabbit board for the last hand, or when the viewer was not
  dealt in.
- The value goes ONLY into the snapshot of that player's own sessions.
  It is never in the shared log, in Result, in another player's snapshot,
  or in stats.
- The TUI shows it next to the rabbit label on the felt, for example
  `┆rabbit · not dealt┆  You would have had: Flush, Ace high`, with the
  5 cards marked. In compact mode, it shows one line under the board. The
  rankings box marks that row with a hollow marker and `Would have: Flush`.
- It goes away at the next deal.

### Build notes

- The felt is at most 46 cells wide, so the line and the label do not fit
  on one line. The line goes on its own line, right under the rabbit
  label, in full mode and in compact mode. The felt gets one more line.
  At 9 seats and 100x30 the table still fits.
- In a narrow felt the line gets shorter, in this order: "You would have
  had: Flush, Ace high", "Would have had: Flush, Ace high", "You would
  have had: Flush", "Would have: Flush". Then the end is cut.
- The room keeps `Room.holes` after the hand ends. `dealtHoles` replaces
  them at the next deal. `Room.holesHand` is the number of the hand of
  those cards. The room computes `Me.RabbitHand` only when
  `Result.HandNumber` is that number, so old cards never make a line.
- The room refuses `Config.RabbitHunts < 0` with `engine.ErrBadConfig`.
- The 5 cards get the same underline as the best-hand hint (`hlHint`), on
  the board, on the rabbit cards and on the own cards.

### Batch 10 audit

The audit of 279ee25 found these issues. Each code fix has a regression
test in an `audit10_test.go` file or in `keepalive_test.go`.

- **P1, an old binary in use.** `bin/stack-overflow` came from 90e7be7,
  before the reset fix 3324ea4, and `scripts/serve.sh` runs that file.
  This is an operation step, not a code change: the host runs
  `make install` from this commit before the next game night. The README
  tells the host to run `make install` after each new commit, shows how to
  read the commit of the binary, and says that other files in `bin/` are
  not in use. The fix did not replace or delete any file in `bin/`.
- **P2 and F1, the old bank after a reset.** `migrateDir` used only "the
  new host key exists" as the sign of an earlier import. A reset that
  deleted the whole folder, or a custom `-hostkey`, then copied the old
  bank again. Now the old bank comes over at most one time. After each run
  with the default state path and an old bank, `migrateDir` writes the
  markers `~/.office-poker/.copied-to-stack-overflow` and
  `~/.stack-overflow/.copied-from-office-poker`. While one exists, the
  bank never comes over. The host key still comes over when it is
  missing. A folder from an older import gets the markers at its next
  start.
- **P3, a reset while the server runs.** The room keeps all banks in
  memory, and each save writes them back, so a deleted `state.json` comes
  back. The new host command `stack-overflow reset -yes` (`ResetState` in
  api.go) takes the state lock, so it refuses while a server runs. It
  moves `state.json` to `state.json.<UTC time>.reset`. That name does not
  end in `.bak`, so `pruneBackups` never removes it. Without `-yes`, the
  command only tells what it does. With the default path, it also writes
  the old-bank markers.
- **F3, the import docs.** README "Files" and §8 above describe the real
  rules now. The README has a "Start fresh (reset)" section with the
  order: stop the server first, then `reset -yes`, and keep the host key.
- **stale-win-on-new-occupant.** `seatInfo` matched `Result.Winnings` and
  `Result.Shown` only by seat. Between hands, a new player in the seat of
  the winner then showed "wins N" and the old hand name. Now an entry
  counts only when the seat was dealt in (`dealtIn`) and the player id of
  the entry is the id of the player in the seat (`samePlayer`; an empty id
  of an old fixture still counts). `revealed` uses the same `samePlayer`.
- **F2, no keepalive.** A client that stopped reading (a Wi-Fi drop
  without a TCP close, or a frozen client) stayed online with its seat
  until the TCP stack of the host gave up, after many minutes on macOS.
  The new `keepAlive` middleware sends `keepalive@openssh.com` with
  want-reply every 15 s (`defaultKeepAlive`). When no answer comes in
  30 s (`defaultKeepAliveWait`), it closes the connection, and the room
  marks the player away as for any disconnect. Each SSH client answers
  this request (a failure answer also counts). `options.keepAlive` and
  `options.keepAliveWait` are for tests; there are no flags. The README
  now says that the server sees a dropped network within 45 s, and
  suggests `ssh -o ServerAliveInterval=15` for the players.
- **F8, `make check` at 279ee25.** `batch2_test.go` compared `Me` with
  `!=`. Batch 10 (acd3f42) already changed it to `reflect.DeepEqual`, so
  `go vet ./...` and `make check` pass. No new change was necessary.

### Final check before the first game

- **SIGHUP during the final save.** After the first Ctrl+C, `run` stopped
  all signal catches, so that a second Ctrl+C kills the process at once.
  A SIGHUP then had its default action too. A tmux pane or a terminal
  window that closes right after the Ctrl+C sends that SIGHUP, and it
  killed the server during the final save: the live check found a
  `.state.json.*.tmp` file and an old `state.json`. Now `run` catches
  SIGHUP and does nothing with it from the first signal until the process
  exits. A second Ctrl+C or SIGTERM still kills the process at once.
  `TestSIGHUPAfterCtrlCSaves` runs the server as a separate process, sends
  SIGINT and then SIGHUP every millisecond, and expects exit code 0, the
  `state saved` line and no temporary file. It failed before the change.
- **SIGHUP to `scripts/serve.sh` during the final save.** The fix above
  did not cover `scripts/serve.sh`. The script runs the server in its own
  process group (`set -m`), so a closed terminal window or tmux pane sends
  its SIGHUP to the script, not to the server. `on_signal` sent a SIGINT
  to the server for each INT, TERM or HUP. The second SIGINT came after
  the server stopped its catches, and it killed the server during the
  stop (exit code 130). A closed window sends SIGHUP two times (from the
  kernel and from the shell, or from tmux), so a close with no Ctrl+C
  also killed the server before the final save. Now `on_signal` sends a
  SIGINT only for the first signal. After that, a SIGHUP sends nothing,
  and only a second INT or TERM sends the forced SIGINT, with a log line.
  `TestServeScriptSIGHUPDoesNotStopTheSave` runs `scripts/serve.sh` with a
  fake server that has the signal rules of `run` and a 1 s save. It sends
  INT then two HUPs, HUP then HUP, and TERM then HUP, and expects the save
  and exit code 0. It failed before the change.
  `TestServeScriptSecondCtrlCForcesTheStop` checks the forced stop.
- **Double log lines after a closed terminal.** The live check of the fix
  above found each line after a window close two times in `server.log`.
  bash keeps a failed write to the closed terminal in its output buffer,
  and the next write to the log file writes that line again. `stamp` and
  `note` now write through `out`. After the first failed terminal write,
  `out` writes only to the log file, and it writes the old buffer to
  `/dev/null`. `TestServeScriptClosedTerminalDoesNotDoubleLogLines` gives
  the script a stdout that is open only for reading, so each terminal
  write fails, and expects each line one time in the log. It failed
  before the change.

## Batch 11: highlight new chat messages

- A chat message from another player is highlighted for 5 seconds after
  it reaches this screen. The highlight then goes away without any key
  press.
- **Highlight:** the whole message (all of its wrapped lines) gets a soft
  background colour that works in dark and light terminals, and a `▌`
  marker at the left edge. In a terminal with no colour, the line is bold
  and keeps the marker.
- **Clock:** the TUI uses the time when its model first saw the message,
  not `ChatLine.At`, so a clock difference between the server and the
  client does not matter. The messages in the first snapshot of a
  connection get no highlight.
- **Own messages** get no highlight.
- **Where:** the chat panel in full and compact mode. When the chat tab
  is not open, the existing unread count shows on the tab. In addition,
  the tab name "Chat" uses the highlight style for the same 5 seconds.
- The panic screen shows no highlight. A message that comes during the
  panic is highlighted for 5 seconds after the player leaves the panic
  screen, if it is still within its 5 seconds. Otherwise it gets no
  highlight.
- A tick message ends the highlight. The TUI starts it only while a
  highlight is on screen, so an idle screen sends no extra frames.
- The key a player presses, scrolling, and new snapshots never extend a
  highlight.

## Batch 12: P copies the screen to the clipboard

- `P` (or Shift+P) on the table screen sends the player's current screen
  to the player's own clipboard. When the help shows, P closes it and
  copies the table.
- **Text:** `View()` before the notice, with the escape codes removed,
  the spaces at the end of each line removed, and the empty lines at the
  top and the bottom removed. It sits in a ``` block with a newline at
  the end.
- **Transport:** a new field `tui.Alert.Clipboard`. The SSH layer writes
  OSC 52 (`ESC ] 52 ; c ; <base64> BEL`) through the same locked writer
  as the frames, so it never comes inside a frame. Base64 has no control
  characters. Text over 256 KiB is not sent. Clipboard alerts have their
  own busy flag, so a bell does not drop a copy.
- **Notice:** "Screen sent to your clipboard · paste with Cmd+V (not in
  the Mac Terminal app)". The terminal gives no answer to OSC 52.
- **Not in:** a text input, the chat, the raise editor (P is the pot
  preset), the panic screen. With no alert hook, P shows an error notice.
- **Help:** the row `M N I P  profile · alerts on/off · hand rankings ·
  copy screen`, so the help still fits 80×24.

## Batch 13: P makes a PNG screenshot with a link

Batch 15 changes the link: the path is `/<token>` with 10 characters
(50 bits), not `/s/<token>.png` with 80 bits. Batch 15 also sends every
link to the chat. See "Batch 15" below.

Batch 12 (the clipboard text) is replaced. The pasted text did not keep
its shape: apps draw the suit and box characters at other widths, or as
emoji. Batch 13 removes `tui.Alert.Clipboard` and the OSC 52 code.

### What the player sees

- `P` (or Shift+P) on the table screen. The same screens as batch 12:
  not in a text input, the chat, the raise editor, or the panic screen.
  With help open, P closes it and shoots the table.
- The TUI takes `View()` at the key press (before any new notice), with
  the size, the background (`Renderer.HasDarkBackground()`) and the
  title `Stack Overflow — <name>` (plus ` · hand #N` during a hand).
  It calls `Options.Shot` from a `tea.Cmd`, never in `Update`.
- While the shot is made, the notice says `Screenshot…`. Then the notice
  says `Screenshot: <url> · Cmd+click to open` for 60 seconds, or until a
  new notice replaces it. The link must never be cut: when the line is
  too narrow, the notice shows only the URL; under that width, the URL
  is still shown in full (the footer can cut the rest of the line, not
  the URL). Also put the last URL in the help screen? No: keep it simple.
- One shot every 3 seconds for each session; a second P within 3 s shows
  `One screenshot every 3 seconds`. Only one shot runs at a time for
  each session.
- An error (nil hook, render error, store error) shows
  `Screenshot failed: <reason>` as an error notice.
- The help row stays `M N I P  profile · alerts on/off · hand rankings ·
  screenshot` (fits 80x24).

### The PNG (internal/shot, Render)

- A terminal cell grid: parse SGR from the view (x/ansi has a parser),
  with 16 colours (one palette for dark, one for light), 256 colours
  (xterm), 24-bit colours, bold (brighter and heavier), faint (mixed
  with the background), italic (may draw as regular), underline,
  reverse, reset. Ignore other escapes (OSC, cursor moves).
- Font: Go Mono (`golang.org/x/image/font/gofont/gomono` and its bold),
  at a size that gives a crisp image on a Retina screen (about 2x: a cell
  of about 18x36 px). Every rune that the TUI can draw must render: box
  drawing and block elements are drawn by code; suits ♠♥♦♣ and the
  other symbols come from Go Mono; a rune that no font has is drawn as
  an empty box, never dropped (so the columns hold). A test lists every
  non-ASCII rune in internal/tui/*.go (not tests) and checks each one
  draws something.
- A cell holds a whole grapheme cluster. A combining mark (for example
  in the name "Adébáyọ̀" or "n̈") stays with the rune before it, also when
  x/ansi gives it as its own sequence. The renderer joins what NFC can
  join. Go Mono has no combining marks, so the code draws the common
  ones (in U+0300-U+036F) with their spacing forms (for example "´" for
  U+0301), over or under the base. Marks can reach into the margin. A letter that Go Mono lacks, for example "ọ"
  or "ế", is drawn from its NFD parts. A cluster with a mark that the
  code cannot draw (or an emoji sequence) is one empty box. Joiners and
  variation selectors have no ink. A mark with no rune before it on its
  line gets a cell of its own, on a space.
- Wide runes (East Asian wide, emoji in a player name) take 2 cells
  (`ansi.StringWidth` rules).
- The window frame: a title bar (dark or light to match), three dots
  (red, yellow, green) at the left, the title in the middle, a margin
  of one cell around the grid, rounded corners optional.
- Speed: a 188x55 frame renders in under 300 ms on the laptop. PNG size
  for that frame stays under 2 MB (use the best-speed or default
  compression; flat colours compress well).

### Serving (Store, cmd/stack-overflow)

- New flag `-shot-port` (default 2280; 0 turns screenshots off, and then
  Options.Shot is nil and P says screenshots are off). The HTTP server
  listens on `-host` at that port, with read/write/idle timeouts, a
  header size limit, and only `Store.Handler()`. A bad port (outside
  0-65535, or equal to -port) exits with code 64 like other bad flags.
  When the port is busy, the server logs a warning and runs without
  screenshots (the SSH game must still start).
- The banner prints `Screenshots: http://<lan-ip>:2280` (or `off`).
- The link uses the address that the player's SSH client connected to
  (`s.LocalAddr()`), so a player on the Wi-Fi gets 192.168.1.20 and the
  host gets 127.0.0.1. IPv6 goes in brackets.
- Store limits: TTL 30 minutes, 200 shots, 64 MiB; the oldest go first.
  Put keeps a copy of exact size: the bytes from Render can have a
  backing array of almost 2x their length, and the limit counts bytes.
  Expired shots are removed on put and on get (no extra goroutine
  needed, or one that stops with the server).
- The file name: `stack-overflow-<name>-<yyyymmdd-hhmmss>.png`, with only
  [a-z0-9-] kept.
- Shutdown: the HTTP server stops (Shutdown with a short timeout) when
  the SSH server stops; `poker down` still stops everything.
- The hole cards of the player are in their own shot. Nobody else can
  find the link: the token has 80 random bits, the handler never lists
  shots, and the log never prints a token.

## Batch 14: share a screenshot in the chat

Batch 15 replaces the private/shared rule of this section, its notices
and the key `V` (fix section). The `public` frame (no notice line, no
bank, no rabbit hunt offer) and the 60 s notice for the other viewers
stay. See "Batch 15" below.

Every player can still press P (batch 13). New: when the picture shows
nothing private, the server also posts its link in the table chat.

### When a shot is shared

The TUI decides at the key press, from the snapshot that the frame shows
(not later, when the PNG is ready). The shot is **private** (link only for
the player, as in batch 13) when any of these is true:

1. A hand is live (`Table.InHand`). This includes an all-in run-out.
2. `Me.RabbitHand` is set: the "You would have had" line is on screen.
3. The frame shows a hole card of the viewer that the other players do
   not see now: for example the viewer folded or mucked, and their own
   seat still shows their cards on their own screen. Cards that went to
   the showdown, or that the player showed with 1/2/3, are public. Check
   in the TUI and the snapshot how the own seat draws the cards between
   hands, and compare with what a spectator's snapshot shows for that
   seat. The rule is: **the shared PNG never shows a card that a
   spectator cannot see at that moment.** If the TUI cannot be sure,
   the shot is private.
4. Any other text on the table screen that only this viewer sees (check
   the screen for such text, for example a best-hand hint under own
   cards, or "Hand #N · new cards"). The help box (P closes it first),
   the chat and the log are public.

Some viewer-only text does not make the shot private. Instead, the
shared frame is drawn without it (the TUI draws the frame again with
`model.public` set):

- The notice line ("Hand #N · new cards", the link of an earlier shot).
- The viewer's bank (`Me.Bank`, no other snapshot holds it): " · bank N"
  in the header of a viewer with no leaderboard row, "(bank N)" or
  "(you buy in for N)" after "S Sit down", and "from bank" or the top-up
  split after "B Buy back in for N". The net in the header stays: the
  leaderboard shows it.
- The rabbit hunt offer "H rabbit hunt (N left)" (`Me.RabbitsLeft`) in
  the board box and the key hints. The frame shows no H offer at all,
  because its presence alone tells whether a hunt is left.

The buy-in picker, the profile box (it shows the bank) and the dashboard
are never in a frame: P does nothing in the picker and the dashboard,
and closes the profile box first.

Otherwise the shot is **shared**.

### What happens

- **Shared:** after the hook returns, the TUI calls
  `Session.ShareShot(links.Table)` in the same `tea.Cmd`. The player's
  notice: `Screenshot shared in the chat: <links.Own> · Cmd+click to
  open`. If ShareShot fails (rate limit), the notice says
  `Screenshot: <url> (not shared: <reason>)`, and the link is not lost.
- **Private:** the notice is as in batch 13, with the reason:
  `Screenshot (only you see it, a hand is on): <url>` or
  `Screenshot (only you see it, your cards are on it): <url>`. The URL is
  never cut; the words go first when the line is narrow (as batch 13).
- **Other players:** a chat line with `Shot` from another player shows
  in the chat panel as `<name> shared a screenshot` and the link on the
  next line (cut with … if the panel is narrow; the notice is the place
  to click). When a new one arrives (not in the first snapshot of a
  connection, not the viewer's own), the viewer gets the notice
  `<name> shared a screenshot: <url> · Cmd+click to open` for 60 s, unless
  the viewer types (chat, rename) or is on the panic screen (then no
  notice; the chat line is enough). The batch 11 highlight applies to the
  chat line as to any message.
- `Links.Table` uses the office-network address that the banner shows,
  found again for each shot (the Wi-Fi address can change). The host's
  own link stays 127.0.0.1, but the chat gets the office address, so
  the other players can open it.
- ShareShot validates the link (ErrBadShot): it starts with "http://",
  has no space or control character, and has at most 120 bytes. It uses
  the chat rate limit. The chat ring keeps the line like any other
  message; after 30 minutes the link gives 404 (the store TTL).
- The 404 page body says: `This screenshot expired or does not exist.`

### Batch 14 fix: no cut link in the chat panel

- A player Cmd+clicked a shared link in the chat panel and got 404. The
  panel cut the link with `…`, and the terminal opened the cut text.
- The chat panel now shows the link only in full. When it does not fit,
  the line says `V shows the link`.
- New key `V`: the last link notice (own or shared) shows again for 60
  s. After 30 minutes (the store TTL), V says the link expired. With no
  link, V says that P takes a screenshot.
- The help row is `M N I P V  profile · alerts · hand rankings ·
  screenshot · its link` (fits 80x24).

## Batch 15: every screenshot goes to the chat, after the hand

The host asked for a simpler rule. Batch 15 replaces the private/shared
rule of batch 14 and the V key.

### The rule

- P works at any time on the table screen (the same screens as batch 13).
- The player gets the own link at once in the notice line, as now.
- The link always goes to the chat:
  - When no hand is live: at once.
  - When a hand is live: the server keeps it, and posts it when that hand
    ends, together with the result (`Session.ShareShot` returns
    later=true). The order of the lines is the order in which the links
    reach the server (see "Order of the lines" below).
- A picture that a player takes during the hand shows the own hole cards
  (also folded ones). It goes to the chat only after the hand, like the
  show choice after a hand. This is the host's choice: the player
  decides by pressing P.
- The shared frame still leaves out the own bank and the rabbit hunt
  offer (the batch 14 fix `public`); that stays.
- Remove: the private decision `shotPrivate` and its reasons and
  notices, the key V, `lastShotLink`, `shotLinkKeyHint`, and their tests
  and docs. The help row goes back to `M N I P  profile · alerts on/off ·
  hand rankings · screenshot`.

### Notices for the player

- No hand live: `Screenshot shared in the chat: <url> · Cmd+click to open`.
- Hand live: `Screenshot: <url> · goes to the chat when the hand ends`.
- ShareShot error (chat rate limit, ErrShotsWaiting): `Screenshot: <url>
  (not shared: <reason>)`. The link is not lost.
- The URL is never cut (batch 13 rules).

### Short links that fit the chat panel

- Path `/<token>` with a 10-character token (50 bits), for example
  `http://192.168.1.9:2280/k3jq7xw2mf` (34 characters; 35 with
  192.168.1.20). With a TTL of 30 minutes and at most 200 shots, a guess
  is not practical on the office network.
- The chat panel draws the link line with no indent. When the link does
  not fit, the line says `(link too long for this panel)` in dim text,
  never a cut link. Check the widths of the chat panel in full mode at
  140, 160 and 188 columns and in compact mode, and report them; the
  usual office links must fit at 160 columns and wider.
- The other players still get the 60 s notice with the whole link when
  a new shared line arrives (batch 14 rules: not the own line, not the
  first snapshot, not while typing or on the panic screen). Lines that a
  hand end posts are new lines, so they get the notice too.

### Server

- `Room` keeps the waiting links in order: (session key, name, url).
  They post at the end of the hand, in the same step that logs the
  result, before the next deal. A server stop drops them. Use the name
  of the player at the time that the link reaches the server.
- The waiting links do not use the chat rate limit when they post; the
  press does (so a press during a hand counts as a chat message then).

### Batch 15 build notes

- `Session.ShareShot` checks the link (ErrBadShot) first. During a live
  hand (`handLive` and `Table.InHand`, also a run-out) it checks the cap
  (ErrShotsWaiting; a refused press does not use the rate limit), then
  the rate limit, and keeps the link. `endHand` posts the waiting links
  after the events of the hand are in the log. Every way that a hand
  ends goes through `endHand`: a showdown, a fold, the end of a run-out,
  a stand-up or a leave that ends the hand. The room has no table reset
  while it runs (`ResetState` works only on a stopped server).
- The chat panel widths (inner, in cells): full mode 26 at 100 columns,
  38 at 140, 42 at 160 and wider (the panel stops at 46). Compact mode
  uses the whole width. A highlighted line loses one cell to the
  marker. So a 35-character link fits in full mode at 140 columns and
  wider. The longest IPv4 link (39 characters, port 65535) fits at 160.
- A panel that is narrower than `(link too long for this panel)` shows
  `(link too long)`.
- Order of the lines. A link reaches the server (`Session.ShareShot`)
  when its picture is ready, not when the player pushes P. The PNG takes
  about 0.1 s to draw at 188x55, and less for a small screen. So when
  two players push P less than about 0.1 s apart, the line of the
  smaller screen can come first. The links of one player always keep
  the order of the pushes: one player has one shot at a time, and one
  every 3 seconds. The contract gives `ShareShot` no press time, so the
  room cannot sort by press. The order "in which the links reach the
  server" is the rule; nothing is lost, cut or posted at a wrong time.
- A waiting link of a player who changes the name during the hand keeps
  the old name. The TUI finds the own line by name, so that player gets
  the notice of the other viewers for the own line.
