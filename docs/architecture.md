# Architecture

How pimvault is built today and why. Behavior is in [requirements](requirements.md); each
structural choice links the ADR behind it.

## Components

One binary, `cmd/pimvault`, calls `internal/cli`; everything else is an `internal/` package with
one responsibility ([ADR 0014](adr/0014-one-module-code-adapted-from-onedrive-go.md)).

| Package | Owns |
|---|---|
| `cli` | The command line: flags, logging, exit codes, the commands, progress and status lines. Wires the other packages together. |
| `config` | Directory resolution (flag > environment > `config.toml` > XDG) and loading `config.toml`. |
| `account` | Account identity (`provider:email`) and the registry: the set of sign-in files. |
| `login`, `oauthflow`, `tokenfile` | Provider OAuth settings and finding the signed-in account; the paste-URL PKCE flow and the token source that saves refreshed tokens ([ADR 0004](adr/0004-own-oauth-apps-paste-url-sign-in.md), [ADR 0016](adr/0016-upstream-oauth2-with-saving-token-source.md)); token files. |
| `httpretry`, `googleapi` | Retries with retry-after for every request; what the Google REST clients share ([ADR 0002](adr/0002-plain-rest-clients.md)). |
| `gmail`, `gcal`, `gpeople`, `gtasks`, `graph`, `outlookimap` | One client per provider API, plain REST/JSON (IMAP for Outlook.com writes), and the encoders from provider JSON to `.ics` and `.vcf`. Each has a `…test` fake. |
| `icalfile`, `vcardfile`, `vtimezone`, `wintz`, `mailheader` | Format helpers: iCalendar envelopes and VTIMEZONEs, vCard writing, Windows-to-IANA zones, lenient mail headers. |
| `archive`, `atomicfile` | The archive's files: where items live, case-safe names, long names, atomic writes, the `.deleted` area ([ADR 0007](adr/0007-open-formats-one-file-per-item.md), [ADR 0008](adr/0008-file-names-from-provider-ids.md), [ADR 0010](adr/0010-deleted-items-kept-aside.md)). |
| `state` | Per-account sync state as JSON, and the run lock ([ADR 0013](adr/0013-accounts-in-parallel-one-run-per-state.md)). |
| `backup` | Bringing the archive in line with an account, and `verify`. |
| `restore` | Writing an archive into an account (restore and migrate), with dedupe ([ADR 0011](adr/0011-how-items-are-written.md), [ADR 0012](adr/0012-dedupe-by-standard-keys.md)). |
| `e2e` | The end-to-end tests (`-tags e2e`) and the Key Vault client for the test tokens ([ADR 0019](adr/0019-test-tokens-in-azure-key-vault.md)). |

`oauthflow`, `tokenfile` and `httpretry` import no other pimvault package, so they can move to
their own repository. `script/live/` holds the live tools (test-account tokens, re-sign-in,
seeding the test accounts, the cloud session's vault token); `script/release.sh` builds releases.

## Data

- **The account** is the source of truth ([ADR 0006](adr/0006-account-is-the-source-of-truth.md)).
- **The archive** (`$XDG_DATA_HOME/pimvault/`): `<account>/{mail,calendars,contacts,tasks}/<collection>/<id>.{eml,ics,vcf}`,
  owned by `archive`. Only item files, a `.folder-name` file for cut long names, and
  `.deleted/<account>/<date>/` for items gone from the account.
- **State** (`$XDG_STATE_HOME/pimvault/`): one JSON file per account (change tokens, label and
  folder maps), owned by `state`; rebuildable by a full listing. The run lock lives here too.
- **Config and sign-ins** (`$XDG_CONFIG_HOME/pimvault/`): `config.toml`, read only; one token
  file per account (mode 0600), owned by `account` through `tokenfile`. For Outlook.com an
  account has a Graph token and an IMAP token.

## Flows

- **login**: `login` builds the provider's OAuth config, `oauthflow.PasteURL` prints the sign-in
  URL and takes the pasted redirect, `login` asks the provider who signed in, `account` saves the
  token under that identity.
- **backup**: `cli` takes the lock, resolves the accounts and backs them up at the same time. Per
  account and data type, `backup` asks the provider client for changes since the stored token
  (or a full listing when there is none, it expired, or a Gmail label was renamed;
  [ADR 0009](adr/0009-change-tokens-with-full-listing-fallback.md)), plans where every item
  belongs, then downloads, moves and keeps aside files one at a time, withholding mass deletions.
  A data type that fails leaves its part of the state as it was; the account's state file is
  saved once its data types have run (never in a dry run).
- **verify**: counts per collection in the account and the archive, then reads every file.
- **restore / migrate**: `restore` reads the archive, lists what the target has, matches items by
  the standards' keys and archive IDs, maps folders and collections by role then name, repairs
  copies (never the archive) and writes the rest through the provider's write path.
- **Token refresh**: every request goes through `oauth2.Transport` with `oauthflow`'s saving
  token source, inside `httpretry`, so a retry after a long wait gets a fresh token.

## Concurrency

Accounts run in parallel goroutines; within an account everything is sequential (F83). Each
account has its own state file, archive directory, token source and quota, so they share
nothing but the output, which `cli` orders. One process per state directory, enforced by a lock
file that a crashed run's successor takes over.

## Failure handling

- An item that fails is logged and counted; the run goes on, keeps the change token where the
  provider would otherwise skip the item next time, and exits 1.
- A refused sign-in skips that account (exit 3); others continue.
- Throttling and transient errors are retried with the provider's retry-after; a refused
  refresh is final.
- Every file write is atomic; leftovers of a crashed run are removed at the next backup.
- Lost or corrupt state costs a full listing; a missing archive file is downloaded again.
