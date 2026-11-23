# Requirement audit (2026-10-25)

An audit of `docs/requirements.md` against the code after plans 002 and
003. The gaps it found were fixed (series cap warning, `migrate <from>`
matching, sign-in hint, progress bar). These were checked and left as
they are, with the reason:

- D58: only backup, the regular run, removes leftover temp files. Restore
  and migrate do not: a dry run writes nothing, and neither deletes temp
  files a running backup is writing.
- D65: Outlook events are matched by the UID backup derives from `iCalUId`
  (its "vCal-Uid" part, or the hex ID). Backup and restore agree, and the
  live test restores nothing twice; `uid` brings nothing more for series
  masters.
- D61 with D57: a dry run cannot list size-cap skips; the cap is only known
  from Outlook.com refusing an APPEND, and its value is not verified.
- D68: pimvault's own archives hold nothing Google Contacts cannot take.
- A token revoked during a run fails the remaining items (exit 1, not 3);
  revocation is found at the start of the next run.
- D50: a full listing fetches contact photos again (files are rewritten
  only when changed); not measured as a cost.
- D77: calendars, contacts and tasks count progress in collections, not
  items.
- D178 for mail folders: mail directories get no " (2)" suffix for names
  that differ only in case. Gmail refuses such labels (seen live, see
  2026-10-25-restore-edge-cases.md), and so does Outlook.com: creating a
  mail folder named like an existing one in other case answers 409
  ErrorFolderExists (seen live 2026-10-26 on the test account).
