# 0012. Dedupe by the standards' keys, plus pimvault's archive IDs

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D15, D37, D40, D41, D60, D65, D160, D167, D169, D176, D177, D184, D192

## Context and problem

Restore and migrate merge into a target that may already hold some of the items, and must be
safe to run again (PV-151, PV-152). They need a way to tell that an item is already there.
Providers rewrite items on the way in: Gmail replaces invalid Message-IDs (F52), Outlook.com
hides contacts it takes for duplicates (F49, F67, F68), Google Tasks has no field to hide an ID
in.

## Decision drivers

- Re-runs write nothing twice.
- Items already in the target before pimvault are not duplicated.
- No custom hashing scheme where a standard key exists.

## Considered options

### A custom content hash for every item

- Good: one rule for every type.
- Bad: providers change content on the way in, so the hash of the copy differs.
- Failure modes: every re-run writes everything again.

### The standards' keys, with pimvault's archive ID where a provider can hold it

- Good: `Message-ID` and iCalendar/vCard `UID` are what other tools use; a hidden archive ID
  makes re-runs exact.
- Bad: a rule per provider quirk.
- Failure modes: a quirk not yet seen causes duplicates until it is recorded (CLAUDE.md, provider
  quirks).

## Decision

Dedupe uses the keys the standards define (mail `Message-ID`, iCalendar and vCard `UID`), not
a custom hash (D15), and happens only when writing into an account (ADR 0006). If the target has
data, restore and migrate merge and skip what it has (D60):

- Mail: same `Message-ID`, `Date` and `From` (D37). Mail without a Message-ID was never
  deduped (D40); restore and migrate now repair the copy they write, never the archive (Date
  from the newest Received header, else the file's modification time; a Message-ID derived from
  the content), so re-runs recognise it (D184, F70). A message whose invalid Message-ID Gmail
  replaced counts as there when Gmail has one with the same Date, From and Subject (D167, F52);
  one whose valid Message-ID holds an encoded-word is found by date and sender (D192, F84).
- Contacts in a migrate: skipped when the target already had a contact with the same email
  address before the run; contacts without an address are always written (D41), and two archived
  contacts sharing an address are both written (D169). Outlook.com: a contact identical in the
  fields pimvault writes counts as there; one with a photo is always written (D176); one
  Outlook.com moved out of view is found through AllContacts by its archive ID (D177).
- Events: skipped when the target has an event with the same UID (D65). D65 named Graph's `uid`
  property; backup and restore both use the UID backup derives from `iCalUId`, and the live test
  restores nothing twice (requirement audit, 2026-10-25).
- Items pimvault wrote: the archive item's ID in a hidden field where the provider has one
  (Outlook contacts and To Do tasks, Google contacts); Google Tasks match by title plus due date
  (D160).

## Consequences

PV-152 to PV-161 describe the behavior; each new provider quirk adds a fake, a test and an entry
in `docs/research/api-quirks.md`.

## Confirmation

`internal/restore` tests run each restore twice against the fakes and assert the second writes
nothing (PV-152 to PV-161); the end-to-end test restores into the live test accounts twice.

## Retirement proof

A provider API that reports an item's origin reliably, making the keys unnecessary.

## Reversal plan

Each rule is one matcher in `internal/restore`; removing one re-allows duplicates of that kind
only.
