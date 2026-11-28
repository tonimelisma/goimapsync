# 0002. Plain REST/JSON clients over net/http

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D46, D47, D82, D83, D85, D87, D140

## Context and problem

pimvault reads Gmail through the Gmail API (D46), Outlook.com mail through Microsoft Graph
(D47), Google Calendar through the Calendar API, as JSON converted to `.ics` (D82), and Google
Contacts through the People API, as JSON converted to vCard 4.0 (D83). Each has an official SDK.
The question is whether to use them.

## Decision drivers

- Binary size and dependency count.
- Control over retries and throttling (PV-111).
- One way of calling every API.

## Considered options

### The official SDKs (`google.golang.org/api`, the Microsoft Graph Go SDK)

- Good: typed requests and responses.
- Bad: measured, a program that only builds the Gmail service with `google.golang.org/api` is
  22 MB and pulls about 220 packages (gRPC, protobuf, OpenTelemetry), against pimvault's 11 MB and
  30 at the time; its generated calls do not retry 429 or 5xx (F32). The Graph SDK is large and
  Graph is a plain REST API.
- Failure modes: throttling surfaces as failures unless wrapped anyway.

### Plain REST/JSON with `net/http`

- Good: small; one retry transport (`internal/httpretry`) for every API; only the fields
  pimvault uses are modelled.
- Bad: pimvault writes its own request and response types.
- Failure modes: an API change pimvault does not model is missed until a test or a live run
  shows it.

## Decision

Microsoft Graph (D85) and the Google APIs (D140, revising D87's choice of the official Google
client) are called as plain REST/JSON with `net/http`. `golang.org/x/oauth2` stays for OAuth
(ADR 0016).

## Consequences

- `internal/googleapi`, `internal/graph`, `internal/gmail`, `internal/gcal`, `internal/gpeople` and
  `internal/gtasks` own their request types; fakes in `*test` packages serve the same JSON.
- Every request goes through `internal/httpretry`, which honors retry-after (PV-111).

## Confirmation

- `.golangci.yml`'s depguard allows no Google or Microsoft SDK module.
- Fakes (`gmailtest`, `graphtest`, `gcaltest`, `gpeopletest`) drive the real clients in unit
  tests.

## Retirement proof

An API pimvault needs that is only practical through an SDK (for example a binary protocol), or a
measured cost of maintaining the request types that outweighs the SDK's size.

## Reversal plan

Replace one package's client with the SDK behind the same functions; the callers and fakes would
change for that API only.
