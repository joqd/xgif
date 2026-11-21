package main

import (
	"log/slog"

	tele "gopkg.in/telebot.v3"
)

const (
	msgStart       = "سلام! 👋\n\nلینک GIF موردنظرت از X رو بفرست تا برات دانلودش کنم."
	msgQueued      = "⏳ درخواستت ثبت شد و در صف دانلود قرار گرفت."
	msgDownloading = "⬇️ در حال دانلود GIF..."
	msgUploading   = "📤 دانلود انجام شد. در حال ارسال..."
	msgFailed      = "❌ دانلود انجام نشد.\n\nلطفاً لینک را بررسی کن و دوباره تلاش کن."
	msgQueueFull   = "ظرفیت دانلود در حال حاضر تکمیل شده.\nلطفاً کمی بعد دوباره تلاش کن."
	msgThirdDone   = "🎉 دانلودت با موفقیت انجام شد!\n\nبرای دانلودهای بیشتر لطفاً عضو کانال اسپانسر ما شو.\n\nبعد از عضویت، دوباره لینک موردنظرت رو ارسال کن."
	msgJoin        = "برای دانلودهای بیشتر لطفاً عضو کانال اسپانسر ما شو.\n\nبعد از عضویت، دوباره لینک موردنظرت رو ارسال کن."
	msgInvalidURL  = "لینک معتبر نیست. لطفاً لینک یک توییت از X (توییتر) بفرست."
	msgBusy        = "درخواست قبلی‌ات هنوز در حال پردازش است. کمی صبر کن."
	msgTempError   = "⚠️ مشکلی پیش آمد. لطفاً کمی بعد دوباره تلاش کن."
	msgShutdown    = "ربات در حال راه‌اندازی مجدد است. لطفاً چند لحظه بعد دوباره تلاش کن."

	msgCookiesOK      = "✅ فایل کوکی با موفقیت به‌روزرسانی شد."
	msgCookiesReply   = "ابتدا فایل کوکی (TXT) را بفرست، سپس روی آن ریپلای کن و /cookies را بفرست."
	msgCookiesFailed  = "❌ به‌روزرسانی فایل کوکی انجام نشد: "
	msgBroadcastReply = "روی پیامی که می‌خواهی ارسال شود ریپلای کن و /broadcast را بفرست."
	msgBroadcastBusy  = "یک ارسال همگانی دیگر در حال انجام است."
	msgBroadcastStart = "📣 ارسال همگانی شروع شد..."
)

func (a *App) register() {
	a.bot.Handle("/start", a.onStart)
	a.bot.Handle("/cookies", a.onCookies)
	a.bot.Handle("/broadcast", a.onBroadcast)
	a.bot.Handle(tele.OnText, a.onText)
}

// touch records the sender so they are known to the broadcast list.
func (a *App) touch(c tele.Context) bool {
	sender := c.Sender()
	if sender == nil || c.Chat() == nil {
		return false
	}
	if err := a.db.upsertUser(sender.ID, sender.Username, sender.FirstName); err != nil {
		slog.Error("user upsert failed", "user", sender.ID, "err", err)
	}
	return true
}

func (a *App) onStart(c tele.Context) error {
	if !a.touch(c) {
		return nil
	}
	return c.Send(msgStart)
}

func (a *App) onText(c tele.Context) error {
	if !a.touch(c) || c.Chat().Type != tele.ChatPrivate {
		return nil
	}
	tweetID, err := parseTweetURL(findURL(c.Text()))
	if err != nil {
		return c.Send(msgInvalidURL)
	}
	a.accept(&Job{userID: c.Sender().ID, chatID: c.Chat().ID, tweetID: tweetID})
	return nil
}
