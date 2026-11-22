# Security policy

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately through GitHub's [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
(Security → Report a vulnerability), or by email to the repository owner listed
on the GitHub profile.

Include what you can:

- affected version or commit,
- steps to reproduce, ideally a minimal query or config,
- impact you believe it has (for example: bot token disclosure, message
  deletion in another channel, denial of service).

Expect an initial reply within a week. Fixes ship on `main`; there are no
maintained release branches.

## Deployment hardening

The bot is a self-hosted Discord application. Whoever runs it should:

- keep `DISCORD_BOT_TOKEN` only in `.env` or the process environment — never in
  a committed file (`.env` and `data/` are gitignored for this reason),
- restrict the token's bot permissions to what the bot uses (send messages,
  attach files, embed links, read message history, manage its own messages),
- set `DISCORD_GUILD_ID` to the one guild that should control the bot so that
  invitations to any other guild are inert,
- treat `data/config.toml` and `data/bot.db` as personal data: they contain
  guild channel IDs and, in price alerts, Discord user IDs.

## Scope

In scope: the Go source in this repository, the container build, and the
documented configuration surface.

Out of scope: the upstream `tjekapi` library, Discord's and Tjek's own services,
and anything requiring a stolen bot token or shell access to the host.
