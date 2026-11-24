# pimvault

pimvault backs up personal Gmail and Outlook.com accounts (mail, calendars,
contacts and tasks) to a local folder of standard files, and writes them back:
into the same account (`restore`) or into another one, in either direction
(`migrate`). It is one static binary for Linux, meant to run unattended on a
server.

- Mail is stored as `.eml`, events and tasks as `.ics`, contacts as `.vcf`.
  Folders are directories.
- Each backup brings the archive in line with the account. Only changes are
  downloaded.
- Restore and migrate merge into what the target has and skip what it already
  has. You can run them again to resume.

## Install

Download the binary for your machine (`linux-amd64` or `linux-arm64`) and
`SHA256SUMS` from the
[releases page](https://github.com/tonimelisma/pimvault/releases), then
check and install it (here version v1.2.3 for amd64):

```sh
sha256sum --check --ignore-missing SHA256SUMS
install -D -m 0755 pimvault-v1.2.3-linux-amd64 ~/.local/bin/pimvault
pimvault --version
```

Each merge to `main` whose checks pass is released as the next patch version
(merges in quick succession may share one). Each release
is built by the repository's release workflow on `main` from its tag
alone: no cache, no secret beyond GitHub's own token. It is signed with
GitHub's build provenance attestations (Sigstore, no long-lived key). To
check that a binary was built by that workflow from this repository:

```sh
gh attestation verify pimvault-v1.2.3-linux-amd64 --repo tonimelisma/pimvault \
  --signer-workflow tonimelisma/pimvault/.github/workflows/release.yml \
  --source-ref refs/heads/main
```

The same attestation is attached to each release as
`pimvault-v1.2.3.sigstore.json`; pass it with `--bundle` to verify without
fetching it from GitHub.

Builds are reproducible: on a checkout of the tag, `script/release.sh v1.2.3`
produces byte-identical binaries and `SHA256SUMS` (it uses the exact Go
version in `go.mod`).

To update automatically, poll
`https://api.github.com/repos/tonimelisma/pimvault/releases/latest`: its
`tag_name` is the newest version and `assets` lists the files above. Install
a new version only after it verifies, and only if it is newer than the one
you run.

To build it yourself instead, you need Go 1.27:

```sh
CGO_ENABLED=0 go build -o pimvault ./cmd/pimvault
```

## Sign in

```sh
pimvault login gmail
pimvault login outlook
pimvault account list
```

`login` prints a sign-in URL. Open it on any device and sign in. The browser
then goes to a `http://127.0.0.1/...` address that fails to load. Copy that
address from the address bar and paste it into pimvault. One sign-in covers
backup, restore and migrate. You can sign in to any number of accounts. Each
is named `provider:email`, and commands accept any unique part of that name.

**Gmail sign-ins:** pimvault's Google app is in Google's Testing status. Only
accounts added as its test users can sign in, and Google ends their sign-ins
after 7 days. To back up your own Gmail account until the app is published,
create your own Google OAuth client:

1. In the Google Cloud console, create a project and enable the Gmail, Google
   Calendar, People and Google Tasks APIs.
2. Set up the OAuth consent screen (External) and publish it "In production".
   You don't need Google's verification for your own use: sign-in shows an
   unverified-app warning, but sign-ins no longer expire after 7 days.
3. Create an OAuth client of type "Desktop app" and put its ID and secret in
   `~/.config/pimvault/config.toml`:

   ```toml
   google_client_id = "....apps.googleusercontent.com"
   google_client_secret = "..."
   ```

**Outlook.com:** `restore` and `migrate` write mail over IMAP. Turn on "Let
devices and apps use IMAP" in Outlook.com under Settings > Mail > Forwarding
and IMAP. Backup does not need it.

## Back up

```sh
pimvault backup --dry-run       # count what a backup would do; writes nothing
pimvault backup                 # every signed-in account
pimvault backup user@gmail.example.com  # one account
pimvault verify                 # compare with the accounts, read every file
```

- Accounts are backed up at the same time, each at its own pace.
- Backup prints one summary line per account, in the accounts' order.
- When a run is interrupted, the next run picks up where it left off.
- If a provider's change history has expired, backup lists everything again
  but downloads only what is missing.
- `--dry-run` prints how many items each data type would download, move and
  remove. For Gmail mail it also prints how many bytes. Outlook.com can't
  tell a message's size without downloading it, so its size shows as
  unknown.

**Deleted items:** when an item is deleted in the account, backup moves its
file to `<archive>/.deleted/<account>/<YYYY-MM-DD>/` instead of deleting it.
Files there are deleted 30 days later. To recover an item, copy its file
back from there.

**Mass deletions:** if more than a fifth of a folder, calendar, address book
or task list holding at least 50 items was deleted since the last backup,
backup keeps those files. It backs up everything else, warns and exits 2.
Check the account; if the deletions were intended, run
`pimvault backup --allow-deletions`. Items moved between folders don't count
as deletions.

**verify** counts each folder's items in the account and in the archive. It
also reads every archive file, listing any that are empty, cut short or
unparseable. It writes nothing.

pimvault runs once and exits. To schedule it, use cron or a systemd timer.
For example, with a user timer:

```ini
# ~/.config/systemd/user/pimvault.service
[Service]
Type=oneshot
ExecStart=%h/.local/bin/pimvault backup --quiet

# ~/.config/systemd/user/pimvault.timer
[Timer]
OnCalendar=*-*-* 03:00
Persistent=true

[Install]
WantedBy=timers.target
```

```sh
systemctl --user enable --now pimvault.timer
loginctl enable-linger "$USER"   # run the timer while you are logged out
```

Only one run at a time can use a state directory. A second run exits at once
and names the running process.

## Restore and migrate

```sh
pimvault restore user@gmail.example.com --dry-run   # list what would be written
pimvault restore user@gmail.example.com
pimvault migrate user@gmail.example.com user@outlook.example.com
```

- `restore` writes an account's archive back into the same account.
  `migrate` writes it into another signed-in account, Gmail to Outlook.com or
  the other way. Run `backup` first.
- Items the target already has are skipped. Mail counts as already there when
  its Message-ID, Date and From match; events when their UID matches.
- Inbox, Sent, Drafts and Archive go to the target's own folders. The default
  calendar, address book and task list go to the target's defaults. Other
  folders, labels and lists are matched by name, whatever the case, and
  created when missing.
- A name the target refuses or keeps for itself, such as Gmail's "Chats" or
  Outlook's "Junk Email", is written as `<name> (restored)`.
- Restored mail is marked read.
- A message without a Date or Message-ID gets one in the copy written to the
  target. The archive is never changed.
- Messages over Outlook.com's size limit (about 13.8 MB) are skipped and stay
  in the archive.

## Where things are

| What | Default | Override |
|---|---|---|
| Archive | `~/.local/share/pimvault/` | `--archive`, `PIMVAULT_ARCHIVE`, `archive` in config.toml |
| Settings and sign-ins | `~/.config/pimvault/` | `--config`, `PIMVAULT_CONFIG` (the file; sign-ins sit next to it) |
| Sync state | `~/.local/state/pimvault/` | `--state`, `PIMVAULT_STATE`, `state` in config.toml |

The `XDG_DATA_HOME`, `XDG_CONFIG_HOME` and `XDG_STATE_HOME` environment
variables move the defaults. In config.toml, directories must be absolute or
start with `~/`.

The archive is laid out account first:

```
<archive>/gmail_user@gmail.example.com/
  mail/Inbox/  mail/Sent/  mail/Drafts/  mail/Archive/  mail/Work/Projects/
  calendars/Calendar/  calendars/<other calendar>/
  contacts/Contacts/   contacts/<other address book>/
  tasks/Tasks/         tasks/<other list>/
```

- Files are named after the provider's ID. Capital letters are written as
  `~` plus the lowercase letter, so names stay apart on case-insensitive
  filesystems.
- A repeating event and its exceptions share one `.ics` file.
- Archived Gmail mail (in no Inbox, Sent, Drafts or user label) is in
  `Archive`.
- A folder name too long for a directory name (over 255 bytes) is stored
  cut, with a hash added. The full name is in a `.folder-name` file inside
  that directory.
- A Gmail message with several labels is stored once, in the first of Inbox,
  Drafts, Sent, its user labels, Archive.

## Exit codes and logs

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | An error, or an item or account failed |
| 2 | Finished, but items were skipped, or backup kept mass-deleted files |
| 3 | An account needs `pimvault login` again |

Results go to stdout. Logs and progress go to stderr: plain text by default,
or one JSON object per line with `--log-format json`. Use `-q` for warnings
and errors only, or `-v` for details.

## What is not kept

- Read, starred and answered flags. Gmail labels beyond the one directory a
  message is stored in.
- Spam, Trash, Outlook's Junk Email, Deleted Items and Conversation History.
  Google "Other contacts". Calendars shared with you or subscribed by URL.
  Generated calendars (birthdays, holidays). Microsoft To Do's Flagged email
  list.
- Account settings: filters, rules, signatures, vacation replies, aliases.
- Provider fields that have no standard iCalendar or vCard property. Google
  Tasks has no due time, recurrence or reminders, so those are dropped when
  writing into Google.

## Security

The archive and the sign-ins are plain, unencrypted files. Files are created
readable only by you (mode 0600, directories 0700). Anyone who can read them
can read your mail and, with a sign-in file, use your account. Keep them on
an encrypted disk, and encrypt any copy of the archive you keep elsewhere.

## License

MIT; see [LICENSE](LICENSE).
