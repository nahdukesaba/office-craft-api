# Email Notifications — Setup Guide

The backend sends email via plain SMTP using [go-mail](https://github.com/wneessen/go-mail), a small, actively-maintained Go library that mostly wraps the standard library — no heavy dependencies, nothing extra to install on your Windows PC (it's compiled straight into the `office-craft-api.exe` binary you already build and run).

You don't host anything new for this — unlike WhatsApp/OpenWA, there's no separate service to run. You just need SMTP credentials from a mail provider.

## 1. Pick an SMTP provider

Two reasonable options, pick one:

### Option A — Gmail (simplest, free, good for getting started)

1. Use a Google account you're happy dedicating to this (a company Google Workspace account is ideal, so replies/bounces don't land in a personal inbox).
2. Turn on 2-Step Verification: [myaccount.google.com/security](https://myaccount.google.com/security)
3. Create an **App Password**: [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords) → app "Mail", device "Other" (name it "Office-Craft") → copy the 16-character password it gives you.
4. Gmail's SMTP settings: host `smtp.gmail.com`, port `587` (STARTTLS).

Gmail's free sending limit is ~500 emails/day, sent from a personal-style address — fine for internal booking reminders, not something to build a customer-facing product on top of.

### Option B — Brevo (formerly Sendinblue) — free tier, better deliverability

1. Sign up at [brevo.com](https://www.brevo.com/) (free tier: 300 emails/day).
2. Verify a sender domain or email address under **Senders & IP**.
3. Get SMTP credentials under **SMTP & API → SMTP**: host `smtp-relay.brevo.com`, port `587`, a generated SMTP login + key (not your account password).

Better choice if you eventually care about deliverability/branding (mail actually comes "from" your own domain rather than a Gmail address), at the cost of one extra signup step.

Either works with the exact same Go code — only the `.env` values differ.

## 2. Configure

Add to the backend's `.env` (same file as `DATABASE_URL`, `SUPABASE_URL`, etc.):

```ini
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=your-account@gmail.com
SMTP_PASSWORD=your-16-char-app-password
SMTP_FROM_NAME=Office-Craft
SMTP_FROM_EMAIL=your-account@gmail.com
```

(For Brevo: `SMTP_HOST=smtp-relay.brevo.com`, `SMTP_USERNAME`/`SMTP_PASSWORD` from the SMTP tab, `SMTP_FROM_EMAIL` = your verified sender address.)

If `SMTP_HOST` is left blank, the backend doesn't fail to start — it just logs `"email (SMTP not configured, not sent)"` instead of actually sending, so you can keep developing everything else before SMTP is ready.

## 3. Test it before relying on it

A small standalone command is included so you don't have to trigger a real booking status change just to test email delivery:

```powershell
go run ./cmd/test-email you@example.com
```

You should see `Sent successfully...` and get an email within a few seconds. If it fails:

- **`535 5.7.8 Username and Password not accepted`** (Gmail) — you're using your real Google password instead of the App Password from step 1, or 2-Step Verification isn't actually turned on yet.
- **`dial tcp: i/o timeout`** — check Windows Firewall isn't blocking outbound port 587, and that your ISP/network doesn't block SMTP ports (some do, especially residential connections and university networks).
- **`535 Authentication failed`** (Brevo) — you're using your Brevo account password instead of the separate SMTP key from the SMTP tab.

## Next step

This guide only covers sending *a* test email. Wiring real booking-status notifications (approved/in_use/finished, with the reminders you described) into `/bookings/:id/notify` — combining this with the WhatsApp side from the previous guide — is the next and final chunk of this feature.