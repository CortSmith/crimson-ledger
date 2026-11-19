# Install Crimson Ledger

Crimson Ledger has two apps:

- **Crimson Scout** runs on the PC where you play. It reads your combat log and sends it to the server.
- **Crimson Ledger** is the viewer. It shows what the server knows.

Both need a **server address** and an **API key**. Scout uses an *ingest* key. The viewer uses a *view* key.
See [Get an API key](#get-an-api-key).

## 1. Check your download

Every release has a `SHA256SUMS` file. Put it in the same folder as your download and run:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

On macOS use `shasum -a 256 --check --ignore-missing SHA256SUMS`.
On Windows (PowerShell) run `Get-FileHash <file>` and compare the value with the line in `SHA256SUMS`.
Do not run a file whose checksum does not match.

## 2. Install, per system

### Linux

The apps need two system libraries:

| Distribution | Install command |
|---|---|
| Debian, Ubuntu | `sudo apt install libwebkit2gtk-4.1-0 libgtk-3-0` |
| Fedora | `sudo dnf install webkit2gtk4.1 gtk3` |
| Arch | `sudo pacman -S webkit2gtk-4.1 gtk3` |
| openSUSE | `sudo zypper install libwebkit2gtk-4_1-0 libgtk-3-0` |

Then unpack and start:

```sh
tar -xzf crimson-ledger-*-linux-amd64.tar.gz
cd crimson-ledger-*-linux-amd64
./crimson-scout
```

Start the viewer with `./crimson-ledger`.

### Windows

1. Unzip the download.
2. Double-click `crimson-scout.exe` (or `crimson-ledger.exe`).

Windows 11 and current Windows 10 already have the **WebView2 Runtime**. If yours does not, a message tells you so.
Install the *Evergreen Standalone* runtime from <https://developer.microsoft.com/microsoft-edge/webview2/> and start again.

SmartScreen may say "Windows protected your PC". The apps are not signed with a paid certificate.
Check the checksum (step 1), then choose **More info**, then **Run anyway**.

### macOS

macOS 11 or newer. No extra libraries.

```sh
tar -xzf crimson-ledger-*-macos-universal.tar.gz
cd crimson-ledger-*-macos-universal
xattr -dr com.apple.quarantine .
./crimson-scout
```

The `xattr` line is needed because the apps are not notarised by Apple (that costs money per year).
Run it only after you checked the checksum.

## I get a message about missing libraries

On Linux you will see this when a library is missing:

```
crimson-scout cannot start: missing required libraries.

  - libwebkit2gtk-4.1.so.0  (...)
      Debian/Ubuntu: sudo apt install ...

Install the libraries above and start crimson-scout again. (It does not fall back to a browser or a local web page.)
```

Run the command for your distribution from the table above, then start the app again.
The exit code is 78 when this happens.

## Quick self-test

To check that the window and the page work on your machine, run either app with `--selftest`:

```sh
./crimson-scout --selftest
```

It prints `SELFTEST OK` and exits. It does not connect to any server.
On a machine without a screen, run it as `xvfb-run -a ./crimson-scout --selftest`.

## Using the viewer

- **Header:** Map picker, Friendly / Hostile / Neutral toggles, the realm in use and Settings.
- **Search and tracking:** type two or more letters, pick a player to track (up to 8; the oldest is dropped). Each gets a colour, a trail and a chip you can click to focus or remove.
- **Side tabs:** *Nearby* (contacts, hostile first), *Bounty* (kill ranking), *History* (the focused player: fixes recorded, first and last fix, last position, share of fixes per map; click a map to open it) and *Status* (connection state, last update, contacts per group and per map).
- **Settings, Display / Appearance:** realm, orientation, time zone, palettes (colour-blind safe, high contrast, mono), dot shapes and sizes, trail style, map grid and opacity, UI scale. They apply at once and are stored in `viewer.json` (mode 0600).

## Where your settings live

In a folder called `.crimson-ledger` in your home folder (Windows: `C:\Users\<you>\.crimson-ledger`).
The settings file is readable by you only. In the apps the key shows as asterisks. Press the eye button to see it.
To use another folder, set the environment variable `CRIMSON_LEDGER_HOME`.

## Get an API key

- **You run the server on your own machine:** `crimson-api keys create --scope ingest --label my-pc`
  (use `--scope view` for the viewer). The key is printed once. Copy it now.
- **Someone else runs the server:** ask them for a server address and a key.
- **AWS:** the deploy script prints both keys once. See [OPERATIONS.md](OPERATIONS.md).

Paste the address and key into the settings of the app and press **Test connection**.

## Uninstall

Delete the folder you unpacked. To remove your settings too, delete `.crimson-ledger` in your home folder.
