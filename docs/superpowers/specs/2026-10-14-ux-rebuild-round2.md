# UX rebuild — round 2

Date: 2026-10-14. Follows `2026-10-13-ux-rebuild-design.md` (round 1, implemented on
`feat/ux-rebuild`). Inputs: a fresh hands-on review of the rebuilt UI at 1280/768/390, light and
dark (findings `.superpowers/sdd/shared/ux-round2.md`, screenshots `.playwright-mcp/r2/`), and a
whole-branch code review (`.superpowers/sdd/shared/final-branch-review.md`). Decisions below are the
implementer's (the user delegated judgement overnight). Frontend only; the API stays frozen.

## Already in flight (wave A)

Code-review fixes: login redirect accepts only same-origin relative paths; ⋯ menu items 40 px on
phones (in `ui/dropdown-menu.tsx`); backup/restore/tag dialogs on `ResponsiveDialog` +
`ConfirmDialog`; HTTPS state from the registered domain's TLS mode; unsaved-edits guard on the
Configuration tab; dead code, token colours, duplicate helpers, docs.

## Wave B — bugs (P0)

1. **A failed create never strands the user.** Every create flow (compose, Git, template) navigates
   to the new app's page as soon as the app exists, even when the first deploy fails; the Overview
   shows a banner "Couldn't start: <first meaningful line of the error>" with "Show details"
   (full output in a scrollable, wrapping code block) and the status reads **Failed** (new
   `StatusPill` tone bad) until a later status report says otherwise. A create that fails before the
   app exists stays on the form with the error as an inline message (wrapped, never overflowing,
   above the sticky bar). Pressing Create twice can't create two apps (button busy until the
   request settles; a second click while a create for the same name is pending is ignored).
2. **No default port clash.** The compose prefill publishes a free host port: the first of
   8080–8999 not published by another app's compose file on this server (parse the `ports:` short
   syntax from the app list's compose files when available; otherwise a random port in that range).
   The template editor's default compose uses `${PORT}` with a `PORT` parameter (default 8080).
3. **Raw errors wrap.** Every place showing agent/Docker output (`ErrorText`, toasts' descriptions,
   create errors) wraps (`break-words whitespace-pre-wrap`), is capped in height with its own
   scroll, and never sits under a sticky bar (add bottom padding equal to the bar on phones).
4. **Template create names the app.** The template form has an "App name" field prefilled from the
   template's app name; if that name is already used on this server, prefill "<name> 2" etc.
5. **Renamed apps show their new name everywhere** — "Apps using it" reads names from the live app
   list (`appLabel` of the current app), not the instance record.
6. **Stored secret values are never revealed or round-tripped as text.** A stored secret variable
   shows "Secret · set" with a **Replace** button that opens an empty input; "Show value" exists only
   while typing a new value. The `<encrypted>` placeholder is never shown, never editable, and a
   value that starts with it is rejected client-side. Test the save payload: untouched secret →
   placeholder; replaced → new ciphertext.
7. **Logs on phones:** chips scroll horizontally in one row; log lines wrap at whitespace and break
   only very long tokens (`overflow-wrap:anywhere` on the line text, not on the timestamp/container
   columns), and the container column collapses into a coloured dot + name above the line.
8. **Apps subtitle counts apps, not containers** ("3 running · 1 stopped").
9. Small bugs: S3 storage name error only after touch/submit; checkboxes render square (fix the
   vendored checkbox only if it is rounded-full; otherwise the call sites); long dialogs keep the
   footer visible (scroll the body, sticky footer); dialogs focus their first field/choice, not the
   close X, and the X focus ring isn't clipped; palette "Back up now" only for apps with runnable
   backups; template cards use the template's icon when the summary has one; one avatar-initials
   helper; the System theme item has an accessible name; password fields set `autocomplete`
   (`current-password` / `new-password`); a direct load of `/settings` on desktop opens the active
   server's General; the org "Icon" label aligns with its control.

## Wave C — simplifications (P1)

1. **Where does my app live?** Overview → Access lists, besides domains, every published port bound
   to all interfaces as `http://<server IP>:<port>` links (parsed from the compose file the Overview
   already reads; ports bound to 127.0.0.1 are omitted). The header's primary button is **Open ↗**
   when the app has an address (first domain, else first published port); Restart moves into ⋯
   (Start stays primary while stopped). App cards show the domain when there is one and otherwise no
   address line (no "No public address").
2. **Configuration bar:** desktop matches phones — when clean, only "History"; when edited, a sticky
   bar "Unsaved changes · Discard · Save draft · Save & redeploy" (Save & redeploy primary).
3. **Hide what can't be used:** Domains section absent without a web proxy (no explanatory card on
   the create form either, one quiet line at most); empty template sections hidden;
   "Backups of deleted apps" link only when there are any; single-server installs drop "push",
   "All servers", "Send again" wording and the Applies-to control; alert rules show channels only
   when they differ from the defaults.
4. **History speaks plainly:** map commit subjects to plain labels — first commit "Created",
   "save <name>" → "Changed <files>" when the changed files are known, else "Saved", "update backups"
   → "Changed backups", "rename" → "Renamed", "rollback" → "Rolled back to <date>"; unknown subjects
   show as-is.
5. **Templates:** the template page header gets a primary **Create app** (→ template flow with this
   template preselected); the create form labels parameters by their description (name shown small,
   "Secret" as a tag); "Save as template" turns literal published host ports into a `PORT` parameter
   and carries the app's backups as backup defaults.
6. **Compose form:** detect `${VARS}` used in the files that aren't defined and offer "Add N
   variables" (one click adds empty rows); the built-in variables list collapses behind
   "Built-in (8)".
7. **Docker → Networks:** group by app like Volumes ("Website", "Notes", "Other"), built-in networks
   hidden behind a toggle, ids only on hover.
8. **Phone rows:** the ⋯ button stays on the title line; long variable/volume names wrap instead of
   being cut.
9. **Backups:** on an app without volumes or bind mounts, the Backups empty state says "This app
   keeps no data on disk, so there's nothing to back up." (no dialog); the add-backup default
   schedule is daily.
10. **Monitoring:** KPI tiles keep the number and the "of total" line, drop sparklines and delta pills
    (the charts below carry the trend).
11. **Inbox rows:** fired = red alert icon, resolved = green check (same slot); unread = bold title +
    blue dot at the far left; the server name shown once; "Only in the Inbox" never per row — one
    hint under the header when the org has no channels ("Alerts only show here. Add a notification
    channel →").
12. **Sign-in:** the snowflake logo above the heading; "Sign in" wording everywhere; terms footer
    only when `VITE_APP_MODE=distributed`; brand spelled **Winterflow** in UI copy; failed login reads
    "Wrong email or password."
13. **One New-app chooser:** the page `/server/:id/new` without `from` renders the same
    `NewAppChoices` component as the dialog (already shared) — ensure identical copy/layout; the
    dialog is the only entry from buttons.
14. **Agent updates:** show the running version; the version field moves under "Advanced" with a
    one-line explanation ("Installs a specific release from GitHub").
15. **Phone Overview order:** Status, Access, Backups, Recent changes, then Resources.
16. **Overlays lighter:** dialog/sheet/palette overlay `bg-foreground/25` with `backdrop-blur-[2px]`
    in light, `bg-black/50` in dark; a confirm opened over a sheet doesn't double-darken (only the
    top overlay is tinted).

## Wave D — polish (P2)

The P2 list in `ux-round2.md` plus remaining ux-notes residuals: mobile top-bar title flash,
app card address tap height, Template section in app Settings when the instance is gone,
Overview backups error links to the Backups tab, entrance animations ≤150 ms and
reduced-motion-safe, consistent sentence case, dark-mode contrast fixes listed there.

## Delivery

Waves B, C, D each ship as focused commits with tests, then a task review; one whole-branch review
and a browser pass at 390/768/1280 light+dark at the end; screenshots in the PR.
