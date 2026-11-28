# Live incidents

Every time pimvault lost, duplicated or misreported data, or touched the
wrong account, in a live run (D107). Newest first. Each entry says what
happened, the impact, the cause, the fix and what keeps it from coming back.

## 2026-10-25: Outlook backup lost items on a case-insensitive disk

- **What:** the end-to-end test on a case-insensitive filesystem (APFS) backed
  up the Outlook test account without errors, but verify found the archive short: Inbox 315 of
  336, Sent 310 of 341, Calendar 199 of 212.
- **Impact:** silent loss of about 5-10% of Outlook items in any archive on
  macOS, exFAT or most SMB shares. Test account only.
- **Cause:** archive files were named by Outlook item IDs, which are
  case-sensitive and often differ only in case; such files overwrote each
  other (F69).
- **Fix:** capitals marked in file names, folders kept apart by case (D178).
- **Prevention:** unit tests for IDs and folders differing only in case; the
  end-to-end test was rerun on that filesystem and passed.

## 2026-10-25: Outlook contacts written again on every run

- **What:** each Gmail → Outlook migrate reported the same 26 contacts as
  written; none stayed in the account.
- **Impact:** misreported counts and endless rewrites; no data lost.
- **Cause:** Outlook.com moves new contacts it takes for duplicates out of
  the contact folders (F49, F67, F68).
- **Fix:** identical contacts count as already there (D176); moved
  contacts are found through AllContacts (D177).
- **Prevention:** fakes reproduce both behaviours; the live migrate dry run
  shows 0 to write.

## 2026-10-25: IMAP setting off reported as "sign-in needed"

- **What:** the first live mail migrate into Outlook.com failed with "sign-in
  needed" although the sign-in was fine.
- **Impact:** a misleading message; nothing written.
- **Cause:** Outlook.com refuses IMAP while the account's "Let devices and
  apps use IMAP" setting is off, which is the default (F66).
- **Fix:** pimvault names the setting (D175).
- **Prevention:** the fake IMAP server answers like Outlook.com with IMAP off.

## 2026-10-25: a test sign-in picked the maintainer's own account

- **What:** `pimvault login outlook` signed in the maintainer's own Microsoft
  account, because the browser already had its session.
- **Impact:** none: the following migrate refused to run because the test
  account was not signed in, and that sign-in was removed at once.
- **Cause:** a browser with several Microsoft sessions picks the active one.
- **Fix:** instructions for test sign-ins say to use a private window and
  check the `signed in as` line.
- **Prevention:** test sign-ins are given with a private window; `login`
  prints the account it registered.
