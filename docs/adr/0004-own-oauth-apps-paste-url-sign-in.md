# 0004. pimvault's own OAuth apps, and sign-in by pasting the redirected URL

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D5, D70, D71, D75, D135, D136, D161, D164

## Context and problem

Every API pimvault calls needs an OAuth token for the account. pimvault runs on a headless
server, where no browser can receive a redirect. Google's device-code flow does not allow Gmail
scopes (F24); Microsoft's device-code flow cannot grant Graph and IMAP in one sign-in (F51, F62).
Borrowing another app's client ID is fragile: Microsoft disabled one tool's app ID in 2024 (F3).

## Decision drivers

- One sign-in per account that covers backup, restore and migrate.
- Works on a headless machine with the user signing in on any device.
- No dependence on another product's app registration.

## Considered options

### Device-code sign-in

- Good: nothing to paste.
- Bad: not allowed for Gmail scopes; on Microsoft it cannot cover Graph and IMAP at once.
- Failure modes: a second sign-in for IMAP, or no Gmail at all.

### Loopback redirect with a local web server

- Good: standard for desktop apps.
- Bad: the browser is on another device than the server, so the redirect never reaches it
  without port forwarding.
- Failure modes: a sign-in that hangs.

### Authorization code with PKCE, pasting the redirected address

- Good: works from any device; one flow for both providers.
- Bad: the user copies an address from a page that fails to load.
- Failure modes: a pasted address with the wrong state is refused, and the user signs in again.

## Decision

pimvault uses its own app registrations, a Google Cloud project and a Microsoft Entra app (D5),
created with the provider CLIs by the agent when implementation reached that point; a new app,
not onedrive-go's (D75). Google sign-in prints a sign-in URL; the user signs in on any device and
pastes the redirected `127.0.0.1` URL back (D70). Microsoft sign-in is the same flow (D71);
device code and the `login --paste-url` flag are gone (D164). `login` asks for read and write
permissions at once (D135); an Outlook.com sign-in is one consent that grants everything,
mail writing over IMAP included, its refresh token redeemed once per API (D161, F62). The apps'
client IDs (and Google's non-confidential desktop client secret) are compiled in, and
`config.toml` can override them (D136; PV-230).

## Consequences

- PV-220, PV-221 and PV-230 describe the behavior; `internal/oauthflow` runs the flow and
  `internal/login` knows the providers' endpoints, scopes and client IDs.
- Users who want their own Google client follow the README's steps.

## Confirmation

`internal/oauthflow` tests (PKCE, state check, refused redirects) and `internal/login` tests (one
Outlook.com sign-in grants Graph and IMAP) for PV-220 and PV-221.

## Retirement proof

A provider flow that delivers the same single sign-in without pasting, available to personal
accounts on a headless host.

## Reversal plan

Add another flow beside the paste flow in `internal/oauthflow`; tokens and the account registry
stay as they are.
