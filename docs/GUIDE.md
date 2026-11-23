# Stack Overflow: the full guide

This guide is the reference for hosts and players. For the short version, see the [README](../README.md).

## Platforms and the internet

- **Windows host.** The server does not build on Windows. Run it in
  WSL 2, and set mirrored networking (`networkingMode=mirrored` under
  `[wsl2]` in `%UserProfile%\.wslconfig`, then `wsl --shutdown`). Without
  it, the banner shows the address of the WSL VM, and other computers
  cannot reach it.
- **Linux host.** Open TCP ports 2222 and 2280 in the firewall (if one is
  on). Do not let the computer sleep during a game. The server keeps only
  a Mac awake by itself.
- **Windows players.** Windows 10 and 11 include the `ssh` command (the
  OpenSSH Client feature). Use Windows Terminal or PowerShell.
- **Tailscale or ZeroTier.** Start the server with
  `-host <the host's Tailscale address>`. The banner then shows that
  address in the join command and in the screenshot links. With `-host`,
  the server listens only on that address, so the host joins with it too,
  not with 127.0.0.1.
- **Port forward.** Forward TCP 2222 on the router and set a table
  password (see [Password](#password)), because anybody on the internet
  can then reach the port. Screenshot links (key `P`) carry the LAN
  address of the host, and players outside the LAN cannot open them.
  Start the server with `-shot-port 0` to turn screenshots off.
- **Easy PINs.** The server refuses easy PINs such as 1234, 1111 or a
  year.

## Run the host

You need Go 1.27.4 or later (the `go` line of `go.mod`). With an older Go,
the Go tool tries to download a newer toolchain, and that fails on a
network without internet access.

Build the binary and start it:

```sh
make install          # builds bin/stack-overflow.new, then renames it over bin/stack-overflow
./bin/stack-overflow
```

`make install` replaces the binary in one atomic step. A server that runs
keeps the old binary until you restart it. `make build` only writes
`bin/stack-overflow.new`. `make check` runs gofmt, `go vet` and the tests.
Without `make`, use `go build -o bin/stack-overflow ./cmd/stack-overflow`.

You can also start it without a build step:

```sh
go run ./cmd/stack-overflow
```

Use `make install` (or `go build`) for regular games. On macOS, the
firewall asks again for each new binary, and `go run` makes a new binary
each time.

At start, the server shows the join command for each network address of the
computer, for example:

```
  ♠ Stack Overflow is listening on 0.0.0.0:2222 (version d9daf20)
    blinds 5/10 · buy-in 1000 chips (₹200) · 9 seats · turn clock 60s
    5 chips = ₹1 · state file /Users/you/.stack-overflow/state.json
    3 time extensions of 30s for each player (key E)
    3 rabbit hunts for each player (key H)
    Hands start when a seated player presses G
    Screenshots: http://192.168.1.9:2280

  Players can join with:
    ssh -p 2222 yourname@192.168.1.9
```

To stop the server, push Ctrl+C one time. The server saves the state file,
tells each player "Disconnected", and stops. A second Ctrl+C stops it at
once. A terminal window or tmux pane that closes after the first Ctrl+C
does not stop the save. This is also true with `scripts/serve.sh`. If the
last save fails, the server shows `state NOT saved` with the reason and
stops with exit code 1.

`./bin/stack-overflow -version` prints the version. The startup banner
shows it too.

Run `make install` again after each `git pull` or new commit, before the
next game. A binary from an older commit does not have the newer
fixes. To see the commit of the binary, type
`go version -m bin/stack-overflow | grep vcs.revision` and compare it with
`git rev-parse HEAD`.

Only `bin/stack-overflow` is in use: `scripts/serve.sh` and `make serve`
run it. Other files in `bin/` (for example old builds) are not in use. Do not
run them.

### Game night

For a long game, start the server with the script:

```sh
scripts/serve.sh                      # or: make serve
scripts/serve.sh -admin pin:alice     # the script gives each flag to the server
```

A `pin:` admin must have a PIN profile before the server starts. See
[Admins](#admins).

The table does not deal when players sit down. It waits until everyone is
in, and then a seated player pushes `G`. When a player leaves a heads-up
game for a break, the table waits again. See
[Start the game](#start-the-game).

The script:

- keeps the Mac awake (`caffeinate`). Keep the lid open and keep the Mac on
  power: a closed lid makes the Mac sleep anyway.
- writes the server output, with a time on each line, to
  `~/.stack-overflow/server.log` and to the terminal. Set
  `STACK_OVERFLOW_LOG` for another file.
- starts the server again 2 seconds after a crash. After 5 crashes in a
  row, each less than 1 minute after the start, it stops. A crash of the
  Go program (a panic) ends with exit code 2, and the script starts the
  server again after it too.
- stops when the server refuses the flags (exit code 64). Fix the flags
  and start the script again.
- stops on Ctrl+C. The server saves and stops cleanly. The script does not
  start it again. A closed terminal window or tmux pane does not stop the
  save. A second Ctrl+C stops the server at once, without the save.

The server itself also keeps the Mac awake while it runs, with
`caffeinate -i -s`. macOS does not count the SSH traffic of the players as
activity, so without this a Mac that nobody touches goes to sleep and the
table stops for everyone.

If you close the terminal window, the server gets SIGHUP. It then saves
and stops cleanly, as for Ctrl+C, and the game ends. With
`scripts/serve.sh`, the script gets the SIGHUP and sends one SIGINT to
the server, with the same result. To keep the game when the window
closes, run the server in `tmux` or with `nohup scripts/serve.sh &`.

### Files

The server keeps its files in `~/.stack-overflow/`:

| File | Contents |
|---|---|
| `state.json` | The players, their banks, stats, PIN hashes and the settle-up history. |
| `state.json.<time>.<why>.bak` | Backups of `state.json`. See [Backup and restore](#backup-and-restore). |
| `state.json.<time>.reset` | The old `state.json` after `stack-overflow reset`. See [Start fresh](#start-fresh-reset). |
| `host_ed25519` | The SSH host key. The server makes the key when it is missing. |
| `server.log` | The server output, when you use `scripts/serve.sh`. |

The old name of the game was Office Poker. If a folder `~/.office-poker/`
exists, the server copies its host key, and one time its `bank.json`, to
the new folder. It writes the marker files
`~/.office-poker/.copied-to-stack-overflow` and
`~/.stack-overflow/.copied-from-office-poker` after the copy, and the old
bank never comes back while a marker exists. A new host has no such
folder, and nothing occurs.

## Join a game

Each player types this command in a terminal:

```sh
ssh -p 2222 <name>@<host-ip>
```

- `<name>` is your login name. Without an SSH key, it is also the name of
  your PIN profile.
- `<host-ip>` is the address that the server shows at start.

Use a terminal of 100 x 30 characters or larger for the full layout. At 80 x
24, the game uses a compact layout.

### Start the game

A new table waits for a start. It deals no hand, also when 9 players sit.

1. Push `S` to sit down.
2. Wait until all players sit.
3. Push `G`. Any seated player can do this.

The first hand starts after 3 seconds (or after the `-next-hand` pause when
it is shorter). The log shows `<name> started the game`. After that, hands
follow one another as usual.

- Push `G` when you watch: the game tells you to sit down first.
- Push `G` with fewer than 2 players who can play: the game tells you that
  2 players must sit. A player who sits out or has no chips cannot play.

When a hand is due but fewer than 2 players can play, the table waits
again. This occurs when a heads-up player leaves, sits out or busts. It
also occurs when the period is marked settled, and after a server
restart. Push `G` again when the players are back.

To deal as soon as 2 players can play (the old behaviour), start the server
with `-autostart`.

## Profiles

A profile has a display name, a chip bank, stats and the alert setting. A
profile can have one or more SSH keys, and one PIN login.

### With an SSH key

**First time.** Type `ssh -p 2222 alice@<host-ip>`. The server accepts your
key and does not ask for a PIN. The welcome screen opens:

1. Type the name that other players see (1 to 12 characters). The field
   starts with your SSH user name.
2. Push Enter to save the name. Push Esc to keep the name in the field.

If you played before with a PIN, push Tab (or Ctrl+L) on the welcome
screen. Type your PIN login and your PIN. The server then adds your key to
that profile. Your bank and your stats stay. This is possible only while
your key has no history. You can also link later: push `M` to open your
profile, then push `L`.

**Next time.** Type the same `ssh` command. Your key finds your profile. The
SSH user name has no effect: your display name stays the same.

### Without an SSH key

**First time.** Type `ssh -p 2222 alice@<host-ip>`. The server asks:

```
New player "alice". Choose a 4-digit PIN:
Type the PIN again:
```

The login is your SSH user name in lower case (1 to 32 characters). Then
the welcome screen opens. Pick the name that other players see, and push
Enter. The welcome screen also shows how to make an SSH key:

```sh
ssh-keygen -t ed25519
```

Push Enter 3 times. With a key, you never type a PIN again. To use the key
with your PIN profile, connect with the key one time and push Tab (or
Ctrl+L) on the welcome screen.

**Next time.** Type the same `ssh` command with the same user name. The
server asks:

```
PIN for "alice":
```

- You can try 3 times on one connection.
- After 5 wrong PINs for one login from one computer in 10 minutes, the
  server refuses that login from that computer for 10 minutes. See
  "PIN rules and guesses" below.
- The server keeps only a bcrypt hash of the PIN.
- If your SSH client uses the password method, type the PIN as the password.
  This works only for a login that exists.

Players from Office Poker who had no key get a PIN profile without a PIN.
The first login with that name sets the PIN.

**PIN rules and guesses.**

- A new PIN must not be easy to guess. The server refuses four equal
  digits (`1111`), a run (`1234`, `4321`), a repeated pair (`1212`,
  `6969`), `2580`, `0852`, `1004` and the years `1900` to `2099`. A PIN
  that you chose before this rule still works.
- The limits for wrong PINs count each computer apart:
  - After 5 wrong PINs for one login from one computer in 10 minutes, the
    server refuses that login from that computer for 10 minutes. The owner
    can still log in from another computer.
  - After 20 wrong PINs for one login from all computers in 1 hour, the
    server refuses that login for 1 hour.
- Each wrong PIN shows in the table log, for example
  `Wrong PIN for alice (2 in the last hour)`. The server log shows the
  address of the computer.
- When somebody links a new SSH key to a PIN profile, the table log shows
  `A new SSH key now logs in as alice`. If that was not you, tell the host.
  The host can remove the key (see [Remove a key](#remove-a-key)).

### Names

A display name has 1 to 12 characters: letters, digits, spaces, `-`, `_`
and `.`. Each name is different from all other names. The server compares
names without case. A name uses letters from one script only (for example,
Latin letters with digits are correct). A name that looks the same as
another name is in use: for example, "Јое" in Cyrillic letters is the same
as "Joe". Push `M` to change your name.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `-host` | `0.0.0.0` | The address to listen on. |
| `-port` | `2222` | The SSH port. |
| `-shot-port` | `2280` | The web port for the screenshot links of the `P` key. `0` turns screenshots off. It must not be the same as `-port`. See [Screenshots](#screenshots). |
| `-seats` | `9` | The number of seats (2 to 10). |
| `-sb` | `5` | The small blind. |
| `-bb` | `10` | The big blind. |
| `-stack` | `1000` | The chips for each buy-in and rebuy. |
| `-timeout` | `60s` | The turn clock. `0` turns the clock off. |
| `-next-hand` | `20s` | The pause between hands. |
| `-autostart` | off | Deal as soon as 2 players can play. Without it, the table waits until a seated player pushes `G`. See [Start the game](#start-the-game). |
| `-runout-delay` | `3s` | The pause before each street of an all-in run-out. `0` deals the board at once. See [All-in run-out](#all-in-run-out). |
| `-extensions` | `3` | The number of time extensions that each player gets for one server run. `0` turns the extensions off. |
| `-extension-time` | `30s` | The time that one extension adds to the turn clock. |
| `-rabbit-hunts` | `3` | The number of rabbit hunts that each player gets for one server run. `0` removes the limit. A negative value stops the server with exit code 64. See [Rabbit hunt and show](#rabbit-hunt-and-show). |
| `-chips-per-unit` | `5` | The number of chips for one unit of money (1 or more). An open period that has chips keeps the rate of the state file. See [Money and settle up](#money-and-settle-up). |
| `-currency` | `₹` | The currency symbol for settle-up. |
| `-admin` | (none) | The players that can mark the period settled, with commas between them. Each entry is a credential: `SHA256:<key fingerprint>` or `pin:<login>`. A display name does not work. See [Admins](#admins). |
| `-state` | `~/.stack-overflow/state.json` | The state file. |
| `-hostkey` | `~/.stack-overflow/host_ed25519` | The SSH host key. |
| `-password` | (none) | A shared table password. See [Password](#password). |
| `-version` | | Print the version and stop. |

The server checks the flags before it opens a file or the port: 2 to 10
seats, a small blind of 1 or more, a big blind not less than the small
blind, a stack not less than the big blind, a port from 1 to 65535, a
`-shot-port` from 0 to 65535 that is not the same as `-port`, and an IP
address (or `localhost`) for `-host`. A bad value stops the server with
a message and exit code 64. A port that another program uses stops the
server before it shows the join command.

Example:

```sh
./bin/stack-overflow -port 2299 -sb 25 -bb 50 -stack 5000 -timeout 20s \
  -admin "pin:alice,SHA256:Xb3kq0kXh8p0Yb9JmJcQd2Vq2wF7hZp9mJx0bQ1c4aE"
```

### Admins

An admin can mark the period settled. The host is always an admin. To make
another player an admin, give the credential that the player logs in with:

- `SHA256:<fingerprint>` for a player with an SSH key. The game compares
  the fingerprint exactly.
- `pin:<login>` for a player with a PIN. The login is the SSH user name.
  The game compares it without case.

A display name never makes a player an admin, because each player can
change the name. The server does not start when an entry is not a
credential. It names the bad entry and stops with exit code 64.

A `pin:` entry counts only when the PIN profile with that login exists
when the server starts. Else the first person to create a PIN profile with
that login would become an admin. The server ignores such an entry, and the
startup banner shows a warning. The player must create the PIN profile
first (`ssh -o PubkeyAuthentication=no -p 2222 <login>@<host>`). Then
restart the server.

To find the credentials, stop the server and type this command:

```sh
./bin/stack-overflow accounts
./bin/stack-overflow accounts -state /path/to/state.json
```

The command prints each player with the PIN login and the key
fingerprints. Copy a value into `-admin`. While the server runs, the command
stops with a message. Then ask the player to type
`ssh-keygen -lf ~/.ssh/id_ed25519.pub` (or the name of their key file) and
to send you the `SHA256:...` part.

A player who links an SSH key to a PIN profile logs in with the key after
the link. The game then matches the key, not the PIN login. Give the key
fingerprint in `-admin` too, if that player must stay an admin.

The startup banner lists the admins. When a profile exists, it shows the
name with a short fingerprint or with the login. It shows an ignored
`pin:` entry as `pin:<login> (ignored)`.

## Keys

The bottom lines of the screen show the keys that you can use now. Push `?`
to see all keys. Letter keys work in upper case and in lower case.

### On your turn

| Key | Action |
|---|---|
| `F` | Fold. |
| `C` or `K` | Check, or call. |
| `R` or `B` | Raise, or bet. This opens the amount editor. |
| `A` `A` | Go all-in. Push `A` two times in 3 seconds. |
| `E` | Add 30 seconds to your turn clock. See [Time extensions](#time-extensions). |

### Amount editor

| Key | Action |
|---|---|
| `H` | Half the pot. |
| `T` | Two thirds of the pot. |
| `P` | The pot. |
| `A` | All-in. |
| `0`–`9` | Type the amount. The first digit replaces the amount in the field. The next digits come after it. |
| Backspace | Delete the last digit. |
| `←` `→` | One big blind less or more. |
| `↑` `↓` | The next or the previous pot-size step. |
| Enter | Send the bet or raise. |
| Esc | Close the editor. |
| `C` | Close the editor and check or call. |
| `F` | Close the editor and fold. |
| `E` | Add 30 seconds to your turn clock. The editor stays open. |

A pot-size raise goes to the current bet plus the pot after your call. The
editor keeps the amount between the minimum and the maximum. A typed amount
below the minimum becomes the minimum. A typed amount above your stack
becomes all-in, and the editor shows "ALL-IN" before you push Enter.

### Between hands

| Key | Action |
|---|---|
| `G` | Start the game, when the table waits for a start. Any seated player can do this. See [Start the game](#start-the-game). |
| `H` | Rabbit hunt: show the board cards that did not come. Any seated player can do this after a hand without a full board. Each player gets 3 rabbit hunts for one server run. See [Rabbit hunt and show](#rabbit-hunt-and-show). |
| `1` | Show your left card. The winner of a hand without a showdown can do this. A player who folded in the hand can do this too. See [Show a folded hand](#show-a-folded-hand). |
| `2` | Show your right card. |
| `3` | Show both cards. |

### After you fold, during the hand

| Key | Action |
|---|---|
| `1` | Show your left card when the hand ends. Nobody sees it before then. |
| `2` | Show your right card when the hand ends. |
| `3` | Show both cards when the hand ends. |

Push the same key again to cancel. Push another key to change the choice.

### At any time

| Key | Action |
|---|---|
| `S` | Sit down (when you watch). With chips in your bank, a picker opens. See [Sit down and buy back in](#sit-down-and-buy-back-in). |
| `O` | Sit out, or play again. |
| `B` | Buy back in (when you have no chips). The chips come from your bank first. |
| `L` | Leave your seat. During a hand, the game asks first and folds your hand. |
| `D` | Open the stats screen: the stats, the fun stats and the settle-up. |
| `M` | Open your profile: change your name, turn alerts on or off, see your login type. While your SSH key has no history, `L` in the profile links the key to a PIN profile. |
| `N` | Turn alerts on or off. |
| `I` | Show or hide the hand rankings box (full mode only). |
| `P` | Take a screenshot: the server draws your screen as a PNG image and shows a link to it. Cmd+click the link to open it. The link also goes to the chat: at once between hands, or when the hand ends. See [Screenshots](#screenshots). |
| Tab | Show the chat (the first tab), the log, or the players. |
| `T` or `/` | Write a chat message. Enter sends it. Esc cancels it. The chat highlights a new message from another player for 5 seconds. |
| `?` | Show or close the help. When the help shows, the other keys close it and then do their usual action. |
| `Q` or Ctrl+C | Quit. During a hand, the game asks first. |
| Cmd+Delete / Ctrl+U | The panic key (Cmd+Delete sends Ctrl+U in Ghostty). See [Panic key](#panic-key). While you type, it clears the line. |
| Ctrl+H | The panic key. See [Panic key](#panic-key). While you type, Ctrl+H deletes a character. Use Cmd+H (with the setup below) or F20 to panic while you type. |

### Welcome screen

| Key | Action |
|---|---|
| Enter | Save the name in the field. |
| Esc | Keep the name that the field shows first. |
| Tab or Ctrl+L | Link your SSH key to a PIN profile (type the login and the PIN). |

### Stats screen

| Key | Action |
|---|---|
| Tab or `←` `→` | Go to the next or the previous tab: Stats, Fun, Settle up, and Host (only for the host). |
| `↑` `↓` | Move to a player. |
| Enter | Show the details of the player. |
| `M` | Mark everything settled (host only). Type `yes` to confirm. |
| Esc or `D` | Close the screen. |

## Screenshots

Push `P` (or Shift+P) on the table to take a picture of your screen. The
server draws your screen as a PNG image, with the colours, the cards and
the lines of the table, in a window frame. The image has twice the
detail of a normal screen, so it is sharp on a Retina display.

The notice line then shows a link, for example:

```
› Screenshot shared in the chat: http://192.168.1.9:2280/k3jq7xw2mf · Cmd+click to open
```

The link always goes to the table chat too. See
[Shared in the chat](#shared-in-the-chat).

Cmd+click the link to open the image in your browser. From there, copy
it or save it, and paste it into Slack or WhatsApp. The link shows for
60 seconds. When the window is narrow, the notice shows only the link,
so the link is always complete.

- The link uses the address that your SSH client connected to. A player
  on the Wi-Fi gets the LAN address; the host gets `127.0.0.1`.
- You can take one screenshot every 3 seconds.
- With the help open, `P` closes the help and takes the table.
- `P` does nothing in a text input (there, it is a letter), in the raise
  editor (there, it is the pot preset) and on the panic screen.

**Privacy.** A picture that you take during a hand shows your own hole
cards. The other players get its link only after the hand (see below).
Only a person with the link can open it. The link has a random part of
10 characters (50 bits), the server never lists the images, and the log
never shows a link. Each image expires after 30 minutes, and a server
restart removes all images. The server keeps 200 images (64 MiB) at
most; the oldest go first.

**The port.** A small web server on port 2280 (flag `-shot-port`) serves
the images. It listens on the same address as the SSH server (`-host`).
The startup banner shows it:

```
    Screenshots: http://192.168.1.9:2280
```

`-shot-port 0` turns screenshots off; `P` then says so. When another
program uses the port, the server shows a warning and starts without
screenshots. On macOS, the firewall setting of `stack-overflow` (see
[Firewall on macOS](#firewall-on-macos)) covers this port too. Your
network must let players reach port 2280, as it does for the SSH
port.

### Shared in the chat

Every screenshot goes to the table chat, so that everybody can open it:

- **Between hands:** the server posts the link at once.
- **During a hand** (also during an all-in run-out): the server keeps the
  link and posts it when the hand ends, together with the result. The
  picture shows your hole cards, also folded ones, so the others see it
  only after the hand. You decide when you push `P`.

When several players push `P` during one hand, the links come in the
order in which the server gets them. The server gets a link when its
picture is ready, about 0.1 seconds after the push. So two pushes of
different players that are very close can come in the other order. Your
own links always come in the order of your pushes. You can have 3 links
that wait for the end of a hand. A player who leaves during the hand
keeps the waiting links: they still go to the chat when the hand ends. A
server stop drops the waiting links. The chat line shows the name that
you had when you pushed `P`.

Your notice says which one it was:

```
› Screenshot shared in the chat: http://127.0.0.1:2280/k3jq7xw2mf · Cmd+click to open
› Screenshot: http://192.168.1.9:2280/k3jq7xw2mf · goes to the chat when the hand ends
```

The chat shows a picture as `Priya shared a screenshot`, with the link
on the next line. The usual LAN link has 35 characters, and it fits
the chat panel at 140 columns and wider. When the chat panel is too
narrow for the whole link, that line says `(link too long for this
panel)`: the panel never shows a cut link, because the terminal opens
the cut text as a wrong address. The other players also get a notice
with the link for 60 seconds, also for a link that the end of a hand
posts, unless they type in the chat or a name, or the panic screen is
on.

A picture counts as a chat message for the chat limit (5 messages in 5
seconds) when you push `P`, also during a hand. When the chat limit
stops it, or 3 of your links wait already, your notice says `(not
shared: …)`, and the link stays on your screen.

The chat gets the local-network address of the server (the address in
the banner), also when the host takes the picture on `127.0.0.1`. The
server finds that address again for each picture, because the Wi-Fi
address can change. A picture leaves out your notice line. It also
leaves out your bank (`bank 2,000`, and the `from bank` or top-up text
of `B Buy back in`) and the `H rabbit hunt (2 left)` offer, because only
you see them. Your net shows, because the leaderboard shows it too.
After 30 minutes the link in the chat gives the page "This screenshot
expired or does not exist."

## Panic key

Push **Cmd+Delete** when somebody comes to your desk. It works in Ghostty
with no setup. Ctrl+H and Cmd+H (after the setup below) also work. Every player in the room, also the players who watch, then sees the
same screens:

1. For 1 second: `🤫  Shh! Someone is coming!`
2. Then a dull terminal that runs the tests of an invented project. A new
   line comes about every 0.7 seconds. The text has the default colours of
   your terminal and no word from the game.

The game does not stop. The turn clock runs, and the clock acts for a
player who does not act. When it is your turn, the last line shows
`--- WAIT  TestCheckoutFlow (45s)`, with the seconds that are left.

- Push any key to go back to the table. That key does nothing else, so a
  stray `F` does not fold.
- A panic key on the panic screen does nothing.
- The panic screen rings no bell and shows no desktop notification.
- Nobody sees who pushed the key. Only the server window shows the name.
- Each player can push the key one time in 15 seconds.

**The panic keys.**

- Cmd+Delete works when you do not type. Ghostty sends Ctrl+U (`0x15`)
  for Cmd+Delete by default, and the game takes Ctrl+U as a panic key.
  iTerm2 does the same with the "Natural Text Editing" key preset
  (Settings > Profiles > Keys > Presets). Other terminals: map Cmd+Delete
  to send the hex code `0x15`. While you type in the chat or a form,
  Cmd+Delete clears the line, as usual.
- Cmd+H (with the setup below) works on every screen, also while you type
  in the chat or in a form. The game gets it as the key F20, which no
  common keyboard has.
- Ctrl+H works when you do not type. While you type in the chat, a name,
  a PIN, the `yes` box or the raise amount, Ctrl+H deletes a character.
  Many terminals send the same byte (`0x08`) for Ctrl+Backspace, so a
  word delete in the chat does not hide every screen in the room.

**Set up Cmd+H on a Mac.** macOS uses Cmd+H to hide the window, so the
game never gets it. Map Cmd+H to the escape sequence of F20 (`ESC [34~`):

- Ghostty: add this line to the Ghostty config file:

  ```
  keybind = cmd+h=text:\x1b[34~
  ```

- iTerm2: Settings > Profiles > Keys > Key Mappings > `+`. Push Cmd+H,
  select "Send Escape Sequence" and type `[34~`.
- kitty: add this line to `kitty.conf`:

  ```
  map cmd+h send_text all \x1b[34~
  ```

- WezTerm: add this entry to `config.keys`:

  ```lua
  { key = 'h', mods = 'CMD', action = wezterm.action.SendString '\x1b[34~' },
  ```

- Terminal (the macOS app): it cannot send text for a Cmd key. Use Ctrl+H.

An older setup that maps Cmd+H to the byte `0x08` still works, but not
while you type. Change it to `\x1b[34~`.

The Backspace key on a Mac sends a different byte (`0x7f`), so Backspace
deletes a character and is never the panic key.

## Sit down and buy back in

Your bank holds the chips that are not at the table. When you leave your
seat, your stack goes back to the bank.

Push `S` to sit down. You get the first open seat.

- When your bank is empty, you buy in at once for one buy-in (`-stack`
  chips).
- When your bank holds chips, a picker opens over the table:

```
╭─ SIT DOWN WITH ──────────────────────────────╮
│                                              │
│ ▸ 1  Buy-in  1,000 · 1,450 stays in bank     │
│   2  Whole bank  2,450                       │
│                                              │
│ 1/2 pick · ↑/↓ move · Enter sit · Esc cancel │
╰──────────────────────────────────────────────╯
```

| Key | Action |
|---|---|
| `1` | Buy-in: sit down with one buy-in. The chips come from your bank, and the rest stays in the bank. |
| `2` | Whole bank: sit down with all the chips in your bank. |
| ↑ ↓, Enter | Move the highlight. Enter sits down with the highlighted option. |
| Esc | Close the picker. You stay a spectator. |

The picker has only these two options. You cannot type an amount.
If your bank becomes empty while the picker is open (for example, the host
marks the period settled), the picker closes. Push `S` again to buy in.

When your bank holds less than one buy-in, option 1 takes the whole bank,
and you buy the difference (a top-up). The picker shows the split and the
price of the top-up, for example "300 from bank + 700 top-up ₹350". A
top-up counts as one buy-in.

**Buy back in.** When you have no chips at the table, push `B`. You get one
buy-in again. The chips come from your bank first. You buy only the part
that the bank does not cover. The rebuy counts as a buy-in only when you
buy chips. The status line shows "from bank" when the bank covers all of
the rebuy. When the bank covers only a part, the status line shows the
top-up and its price.

## Time extensions

Each player gets 3 time extensions for one server run. Push `E` on your turn
to use one. One extension adds 30 seconds to your turn clock. You can use
more than one extension in the same turn. The action bar shows `E +30s (3)`:
the time that one extension adds, and the number of extensions that you
have. When you have no extensions, the action bar does not show the hint.

`E` works on the action bar and in the amount editor. In the chat input and
on the welcome screen, `e` is a typed letter.

The server keeps the count for each player, not for each connection. A new
connection does not give new extensions. A server restart gives all players
the full count again. The host sets the count with `-extensions` and the
time with `-extension-time`.

## Your hand

When you have cards and the board has 3 or more cards, the name of your best
hand shows under your cards, for example "Two Pair, Kings and Sevens". The
game underlines the cards that make the hand: your cards and the board
cards. It marks only the cards of the hand type, not the kickers. For
example, with a pair it marks the 2 cards of the pair. The edge of a card
never changes. Other players do not see your hand name.

In full mode, a "HAND RANKINGS" box under the chat lists the 10 poker hands
from best to worst. A `▶` marks the row of your hand now. Push `I` to hide
or show the box.

### A new hand

- The hand number shows next to your cards, for example "Hand #12".
- After a hand, the result stays on the table until the next deal. Your
  cards of that hand are dim and say "last hand · #12". The title of the
  table says when the next hand starts, for example
  "RESULT · Hand #12 · next hand in 7s".
- At the next deal, the board empties, the screen redraws, and for 2
  seconds a notice says "Hand #13 · new cards". The log shows a line
  "── Hand #13 ──".

## Alerts

When it is your turn, the game rings the terminal bell. When 5 seconds are
left on your clock, it rings again. After a time extension, it can ring
again when 5 seconds are left on the new time.

If your terminal window is not in front, the game also sends a desktop
notification, for example "Your turn — Bob raises to 60". Terminals that
show OSC 9 notifications (for example iTerm2, WezTerm and Ghostty) show it.
Other terminals ignore it.

Push `N` to turn the alerts off or on. The game keeps this setting in your
profile.

## Stats

Push `D` to open the stats screen. The Stats tab shows one row for each
player. A dot (●) marks the players that are online.

| Column | Meaning |
|---|---|
| Net | The chips that you won or lost in this period, and the money for them. |
| Hands | The hands that you played. |
| Won | The hands where you won chips. |
| VPIP % | The hands where you put chips in before the flop by choice. Blinds do not count. |
| PFR % | The hands where you bet or raised before the flop. |
| WTSD % | Showdowns divided by the flops that you saw. |
| W$SD % | The showdowns that you won divided by all your showdowns. |
| Biggest pot | The most chips that you won in one hand. |
| Best hand | Your best hand at a showdown. |
| All-ins | The hands where you went all-in. |
| Buy-ins | The number of buy-ins and rebuys. |
| Lifetime net | The chips that you won or lost in all periods. |

A percentage shows "—" when the game has no data for it. On a narrow
terminal, the screen does not show the columns at the end of the list.

The game counts the Fun stats and the Host stats only for hands from this
version onward. Older hands do not have the data for them, so these stats
start at zero.

### Fun tab

All players see the Fun tab.

| Column | Meaning |
|---|---|
| Bad beats | The hands where you were 70% or more to win the main pot when the last chips went in, but you lost the pot or split it. The column also shows your worst beat: your highest chance to win in such a hand. The detail card also shows your cards in that hand. |
| Luckiest river | The most chips that you won in a hand where your hand was behind before the river and the river card made it the winner. The column also shows the river card. |
| First win | The number of hands that you played until you won a hand for the first time. This number does not change later. |
| Nemesis | The player who took the most chips from you, minus the chips that you took from that player. The column shows the name and the chips. |

**Your favourite hand.** A line under the table shows the starting hand
that you played most often by choice, for example "AKs" (Ace-King of one
suit), "QJo" (Queen-Jack of different suits) or "77" (a pair of sevens). It
also shows how many times you played it. Only you see your favourite hand.
Other players do not see it, and the host does not see it. The game never
shows other players anything that comes from cards that nobody showed.

### Host tab

Only the host sees the Host tab. These stats tell how each player plays.

| Column | Meaning |
|---|---|
| bb/100 | The big blinds that the player won for each 100 hands. It counts only the hands that have host stats (from the batch 3 upgrade onward). |
| ₹/hour | The money that the player won (all periods) for each hour in a seat. It counts only the hands and the seat time from the batch 3 upgrade onward. Time sitting out does not count. It shows "—" for less than 10 minutes in a seat. With `-currency ""`, the column is chips/hour and shows chips. |
| VPIP/PFR | The VPIP % and the PFR % from the Stats tab. |
| 3-Bet | Before the flop, one player raised and this player raised again. Percentage of the times that this player had that chance. |
| AF | Aggression factor: bets and raises after the flop, divided by calls after the flop. "∞" means bets and no calls. |
| C-Bet | The player made the last raise before the flop and then bet on the flop. Percentage of the times that the player had that chance. |
| Fold to C-Bet | The player folded on the flop to the bet of the last raiser before the flop. |
| Fold to 3-Bet | The player raised first before the flop, another player raised again, and the player folded. |
| Steal | All players before folded, and the player raised from the cutoff (the seat before the button), the button or the small blind. |
| Limp | Nobody raised before the player, and the player only called the big blind. |
| Check-raise | The player checked after the flop, another player bet, and the player raised on the same street. |
| Biggest loss | The most chips that the player lost in one hand. |
| Win streak | The most hands that the player won one after the other. |
| All-in win % | Of the hands where the player was all-in and went to a showdown, the percentage that the player won. |

Each percentage shows "—" when the player never had the chance. Push Enter
on a row to see each count as "count/chances", with a short explanation.

**Who is the host.** The host is a player who connects from the server
computer: from a loopback address (`localhost`), or from one of the
network addresses of that computer. The server reads the network addresses
again at each connection. An IPv6 link-local address (`fe80::…`) is only
the host when it is an address of the network interface that the
connection came in on. For example, every Mac has `fe80::1` on `lo0`, and a
player on the Wi-Fi who uses `fe80::1` is not the host. The host can also mark the period settled. A
player in `-admin` can mark the period settled, but does not see the Host
tab. See [Admins](#admins).

### Stats on the command line

The host can print the tables and the settlement without a client:

```sh
./bin/stack-overflow stats
./bin/stack-overflow stats -state /path/to/state.json
```

The command prints the Stats table, the Fun table, the Host table and the
settlement. It does not print favourite hands. This command only reads the
state file, so it works while the server runs. The command uses the money
rate that the state file keeps for the open period. `-chips-per-unit` and
`-currency` of `stats` apply only to an older file without that rate. The
command rounds bb/100 and ₹/hour in the same way as the Host tab, so both
show the same numbers.

## Money and settle up

The game uses chips at the table. Each buy-in has a price in money:
`-chips-per-unit` chips are one unit of money. With the defaults, 5 chips
are ₹1, so a buy-in of 1,000 chips costs ₹200.

A period starts when the host marks the last period settled. For each
player:

```
period net = bank + chips at the table (also in a live hand) − chips bought in this period
```

The Settle up tab shows:

1. The balance of each player. Green players receive money. Red players pay.
2. The transfers that settle the period, for example "Bob pays Alice ₹350".
   The game uses the fewest transfers that it can find: it matches the
   largest debt with the largest credit, again and again.
3. The past periods.

The game rounds money to the nearest hundredth (0.5 goes away from zero).
It then gives a remainder of ±0.01 to the largest balance, so the balances
add up to exactly zero.

The game does not move money. The players pay each other.

The state file keeps the rate (`-chips-per-unit` and `-currency`) of the
open period. If you restart the server with another rate while the period
has chips in it, the period keeps its saved rate, because the players
bought their chips at that rate. The server shows a note at start. The new
rate starts with the next period. The history keeps the rate of each
period.

### Mark everything settled

The host can close the period. A player whose credential is in `-admin` can
also close it (see [Admins](#admins)). See [Who is the host](#host-tab).

1. Wait until the hand ends.
2. Push `D`, then go to the Settle up tab.
3. Push `M`.
4. Type `yes` and push Enter.

The game then stands up all players, sets every bank and every period count
to zero, keeps the lifetime stats, and adds the period to the history.
Before it does this, the server makes a backup of the state file. If it
cannot make the backup, it refuses to settle. The table then waits for a
new start: the players sit down again, and one of them pushes `G`.

## Rabbit hunt and show

- After a hand that ends before the river, any seated player can push `H`.
  The game deals the rest of the board from the same deck, with a burn card
  before each street. The cards show in the empty board places, with a
  dashed edge and the label "rabbit · not dealt". Nobody dealt these cards;
  they only show what was next in the deck. The log shows a line, for
  example "Rabbit hunt (Bob): [9♣] [A♥]".
- Each player gets 3 rabbit hunts for one server run. The key hint shows
  the number that you have, for example `H rabbit hunt (2 left)`. A hunt
  counts only when it shows cards. When you have no hunts, the hint goes
  away. If you push `H` then, the status line shows "no rabbit hunts left".
  The other players can still hunt with their own hunts. The server keeps
  the count for each player, not for each connection, so a new connection
  does not give new hunts. A server restart gives all players the full
  count again. The host sets the count with `-rabbit-hunts`. `0` removes
  the limit.
- After a hunt, each player who was dealt into the hand sees a private
  line under the rabbit label, for example "You would have had: Flush, Ace
  high". It is the best hand that your own 2 cards make with the full
  board: the dealt cards and the rabbit cards. A player who folded sees it
  too. The game underlines the 5 cards of that hand: on the board and on
  your own cards. The hand rankings box marks the row of that hand with a
  hollow marker (▷) and shows "Would have: Flush". Only you see your line:
  the other players, the log and the stats never get it. A player who was
  not dealt into the hand sees no line. The line goes away at the next deal.
  On a narrow screen the line is shorter, for example "Would have: Flush".
- When all other players fold, the winner can show cards: `1` shows the left
  card, `2` shows the right card, `3` shows both. All players see the cards
  with a "shown" mark. The log shows a line, for example "Alice shows A♠".
  A hand that showed at the showdown cannot show again.
- A player who folded can show cards too. See
  [Show a folded hand](#show-a-folded-hand).
- You can do these things only in the pause before the next hand. If you
  push `H` or `1`/`2`/`3` near the end of the pause, the game holds the next
  hand so that the cards stay on the screen for at least 4 seconds.

## Show a folded hand

After you fold, you can show one or both of your cards to the table. The
cards never show during the hand. They show only after the hand ends.

- **During the hand**, after your fold, push `1` (left card), `2` (right
  card) or `3` (both cards). The game keeps your choice and shows nothing
  yet. The status line says, for example, "You will show A♠ K♦ when the
  hand ends · press 3 again to cancel". Push the same key again to cancel.
  Push another key to change the choice.
- **When the hand ends**, the game shows the cards that you chose. This
  occurs before the result comes on the screen, and before the next hand.
  If the hand has an all-in run-out, the cards show after the river.
- **After the hand**, in the pause before the next hand, you can also push
  `1`, `2` or `3`. The status line names the card of each key, for example
  "You folded. Show your cards?  1 A♠  2 K♦  3 both". A show after the
  hand occurs at once, and you cannot cancel it. The prompt shows also
  when you sit out, or when the table waits for players.
- Until you show, your own seat draws your folded cards dim. Only you see
  them. The other players and the spectators see no cards.
- The log shows a line, for example "Alice shows A♠ K♦". The seat of a
  player who folded and showed draws the cards with the mark "folded ·
  shown". A narrow seat says "folded" or "shown". When the mark does not
  fit, the seat draws only the cards.
- The footer shows `1 2 3 Show` only when a show is possible.
- If you leave the seat during the hand, the game forgets your choice. If
  your connection stops, the game keeps the choice until the hand ends,
  because your seat stays until then.
- A show does not change the stats.

## All-in run-out

When the betting ends with two or more players in the hand and the board is
not complete (all players but one, or all players, are all-in), the server
does not deal the board at once:

1. All hands in the run-out turn face up for every player and spectator.
   The log shows a line for each hand, for example "Alice shows A♠ A♦".
2. The board header shows "ALL-IN · RUN-OUT", and nobody acts. No turn
   clock runs.
3. The server deals the next street (the flop, the turn, then the river)
   after each pause. The host sets the pause with `-runout-delay`.
4. After the river, the showdown and the pause before the next hand run as
   usual.

If a player in the run-out leaves, the server folds that hand. If only one
player is then left, that player wins the pot at once. The hand of that
player is already face up, so the server does not ask to show it again. If
your connection stops during the run-out, you keep your seat until the hand
ends.

## Merge two players

If one person has two profiles (for example, one with a PIN and one with a
key), the host can merge them:

1. Stop the server (push Ctrl+C).
2. Type this command:

   ```sh
   ./bin/stack-overflow merge "Old name" "New name"
   ```

3. Start the server again.

The merge moves the keys, the PIN login (when the second profile has none),
the bank, the buy-ins and the stats of the first profile into the second
profile. Then it deletes the first profile. You can use a display name or a
PIN login for each profile. Use `-state path` for a state file at a
different place.

If a name is the display name of one profile and the PIN login of a
different profile, the merge stops and changes nothing. Then add a prefix
to select the profile:

```sh
./bin/stack-overflow merge "login:alice" "name:Alice K"
```

`name:` finds a display name only. `login:` finds a PIN login only.

If the server runs, the merge stops with a message and changes nothing.
The merge makes a backup of the state file first.

## Remove a key

`stack-overflow accounts` lists the SSH keys of each profile. To remove a
key from a profile (for example, a key that somebody linked to the PIN
profile of another player), stop the server and type:

```sh
./bin/stack-overflow unlink SHA256:...
```

The key then logs in as a new player. The command refuses to remove the
only way to log in to a profile; merge that profile instead. It makes a
backup of the state file first.

## Backup and restore

The server keeps up to 10 copies of the state file next to it, with the
time (UTC) and the reason in the name:

| File | When |
|---|---|
| `state.json.20261118T190000Z.start.bak` | At each start of the server. |
| `state.json.<time>.settle.bak` | Before "Mark everything settled". |
| `state.json.<time>.merge.bak` | Before `stack-overflow merge`. |
| `state.json.<time>.unlink.bak` | Before `stack-overflow unlink`. |

The server removes the oldest of these copies when there are more than
10. The `.v1.bak` and `.v2.bak` copies of an old file format stay.

Before a game, you can also make your own copy:

```sh
cp ~/.stack-overflow/state.json ~/state-before-game-night.json
```

To restore a copy:

1. Stop the server (push Ctrl+C).
2. Copy the backup over the state file, for example:

   ```sh
   cp ~/.stack-overflow/state.json.20261118T190000Z.settle.bak ~/.stack-overflow/state.json
   ```

3. Start the server again.

The server also shows each failed save in the server log and in the table
log, for example when the disk is full.

## Start fresh (reset)

To start again with no players, banks or stats:

1. Stop the server (push Ctrl+C in its window). Do this first. A
   running server keeps all banks in memory. If you delete `state.json`
   while it runs, the server writes all banks back at the next hand or at
   the stop.
2. Type:

   ```sh
   ./bin/stack-overflow reset -yes
   ```

   The command moves `state.json` to `state.json.<time>.reset` next to it.
   It refuses while the server runs. Without `-yes`, it only tells what it
   does.
3. Start the server again.

The host key stays, so the players get no host key warning. To undo the
reset, stop the server and move the `.reset` file back to `state.json`.
The backup rotation never removes a `.reset` file.

Do not delete the whole `~/.stack-overflow/` folder to start fresh. That
also deletes the host key. The server then makes a new key, and each player then gets a host key
warning.

## Turn clock and disconnects

- On your turn, a bar shows the time that is left. When the time ends, the
  server checks for you if it can, and folds if it cannot. You then sit out
  from the next hand. Push `O` to play again. Push `E` to get more time
  (see [Time extensions](#time-extensions)). After an extension, the bar
  becomes full again.
- If your connection stops during a hand, you keep your seat until the hand
  ends. On your turn, the server checks or folds for you at once after it
  sees the disconnect. Connect again with the same key or login before the
  hand ends to keep your seat.
- The server sees a disconnect at once when your SSH program stops. When
  the network stops without a close (for example, a Wi-Fi drop or a laptop
  that sleeps), the server finds out with a keepalive check. It asks your
  SSH program for an answer every 15 seconds. When no answer comes in 30
  seconds, the server closes the connection. This takes 45 seconds at
  most, so the turn clock (60s) can end first.
- So that your own SSH program also sees a dead server, connect with
  `ssh -o ServerAliveInterval=15 -p 2222 <name>@<host-ip>`.
- If your connection stops between hands, you stand up at once. Your chips
  go back to your bank.

## Connection limits

The server protects itself from clients that do not behave correctly:

- The server accepts at most 64 connections at the same time, and at most 8
  from one address. An extra connection gets a short message and closes.
  OpenSSH shows this message only with `ssh -v`.
- A client must start a session within 2 minutes after it connects. If it
  does not, the server closes the connection.
- One address can make at most 20 new key profiles in one hour. A known key
  always works, and the host computer has no limit. The server does not
  save a key profile that never chose a name, never played and never
  changed a setting, and it removes such profiles at start.
- A run of input of more than 4 KB without a space, or a paste of more than
  4 KB, closes the connection. Normal keys and chat lines are much shorter.
- If a client sends input very fast for a long time, the server reads it
  more slowly. The connection stays open.
- The server log shows one line when a player connects and one line when a
  player disconnects. Control characters in a name or other client text
  show as escape text, for example `\x1b`.

## Password

Without a password, anybody who can reach the port can play. Each player
still needs an SSH key or a PIN.

With a shared password, each player must type it:

- A player with an SSH key proves the key first and then types the password.
- A player without a key types the password first and then the PIN.

Set the password with the environment variable `STACK_OVERFLOW_PASSWORD`, so
that other users of the computer cannot see it in `ps`:

```sh
STACK_OVERFLOW_PASSWORD=secret ./bin/stack-overflow
```

The `-password` flag also works. When you set both, the flag wins.

## Firewall on macOS

When the server starts for the first time, macOS can ask "Do you want the
application "stack-overflow" to accept incoming network connections?".
Click Allow.

If you clicked Deny, or if players cannot connect, do these steps:

1. Open System Settings > Network > Firewall > Options.
2. Find `stack-overflow` in the list, or add `bin/stack-overflow` with the +
   button.
3. Set it to "Allow incoming connections".

Each new build makes a new binary. macOS can ask again after a new build.

The setting is for the application, not for a port. It covers the SSH
port and the screenshot port (2280) together.

Your network must let players reach the port. A guest Wi-Fi network often
blocks connections between computers.

## Limitations

- The game has one room with one table.
- The game shows who pays whom, but it does not move money.
- The game is only as fair as the host. The host can read the server memory
  and the state file.
- A 4-digit PIN is a weak secret, also with the limits for wrong PINs. Use
  SSH keys, or a shared password, if this is a problem.
- The look-alike check for names covers Cyrillic and Greek letters that
  look like Latin letters. It does not cover digits that look like letters
  (`0` and `O`, `1` and `l`).
- Players do not choose a seat. `S` takes the first open seat.
- The log writes a ten as `T` (for example `T♥`). The table shows `10`.
- Some terminals draw suit symbols, box lines, `½` and `⅔` two cells wide.
  In such a terminal, the layout is not aligned. Set "ambiguous width
  characters" to single width.
- Chat messages do not keep the parts of emoji that terminals draw at
  different widths: skin tones, flags, keycaps, joined emoji and the emoji
  style mark. For example, "👍🏽" becomes "👍" and "❤️" becomes "❤". On some
  terminals such characters make a line too wide, and old characters then
  stay on the screen.
- A terminal that does not answer the colour query of the server shows an
  empty screen until you push a key. Normal terminal programs answer at once.
- The turn clock, the pause before each street of a run-out and the pause
  between hands can be up to 1 second late.
- Some terminals send `0x08` for the Backspace key. In such a terminal,
  Backspace is the panic key when you do not type. Set the terminal to send
  `0x7f` (DEL) for Backspace.

## Development

```sh
make check                             # gofmt, go vet, go test -race
go run ./cmd/tuipreview -w 100 -h 30   # draw the screens without a server
```

`DESIGN.md` gives the poker rules, the server rules and the screen
specification.


## License

MIT. See [LICENSE](../LICENSE).
