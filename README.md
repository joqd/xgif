# XGIF

A small Telegram bot that downloads GIFs/animations from X (Twitter) with `yt-dlp` and sends them to users. Single Go binary, SQLite, YAML config, managed by systemd.

## Requirements

- Linux with systemd
- Go 1.22+ (build only)
- `yt-dlp` in `PATH` (for the service, preferably `/usr/local/bin/yt-dlp`)

Install yt-dlp:

```bash
sudo curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
sudo chmod a+rx /usr/local/bin/yt-dlp
yt-dlp -U   # update later
```

## Telegram setup

1. **Bot:** talk to [@BotFather](https://t.me/BotFather), run `/newbot`, and copy the token.
2. **Sponsor channel:** create a channel, add the bot as an **administrator** (needed for membership checks).
3. **Storage channel:** create a **private** channel and add the bot as an administrator with permission to post messages.
4. **IDs:** channel IDs look like `-100...`. Forward a channel post to a bot such as @RawDataBot to read them. Get your own numeric ID the same way.

## Install and update

```bash
git clone <your repo> xgif && cd xgif
sudo bash setup.sh
sudo nano /etc/xgif/config.yaml
sudo systemctl start xgif
```

Run `sudo bash setup.sh` again to update. The binary is replaced and the service is restarted if it was running. `/etc/xgif/config.yaml`, `/etc/xgif/db` and `/etc/xgif/cookies.txt` are never overwritten. A failed build leaves the installed binary untouched.

If `go.sum` is missing, `setup.sh` runs `go mod tidy` once (network required). Commit the generated `go.sum`.

## Configuration

`/etc/xgif/config.yaml`:

```yaml
bot_token: "TELEGRAM_BOT_TOKEN"
admin_id: 123456789
sponsor_channel_id: -1001234567890
storage_channel_id: -1009876543210
max_workers: 3        # optional
max_queue_size: 100   # optional
```

The bot refuses to start if required values are missing or invalid.

## Service management

```bash
systemctl start|stop|restart|status xgif
systemctl enable xgif
journalctl -u xgif -f
```

## Admin commands

Only `admin_id` can use these.

- `/cookies` – send a Netscape-format `cookies.txt` file to the bot, then reply to that file with `/cookies`. The file is validated and installed atomically with mode `0600`. Cookies are optional.
- `/broadcast` – reply to any message with `/broadcast` to copy it to every registered user. A final report shows delivered and failed counts.

## Behavior notes

- Each user gets three free successful downloads, then must be a member of the sponsor channel.
- Downloads are cached by tweet ID in the storage channel; cached sends count toward the quota too.
- One active request per user; identical tweets requested at the same time share one download.

## Tests

```bash
go test ./...
```
# xgif
