# unglued

<p align="center">
  <img src="images/unglued.png" alt="unglued" width="200"/>
</p>

**unglued** is a lightweight paste tool for sharing text and code fast. No accounts, no fuss. Paste your snippet, get a link, done. Syntax highlighting, line numbers, and URL-based selections (e.g. `?hl=10-20`) make it easy to read and reference. Switch between Light and Dark mode right in the viewer.

If you allow it, anyone with the link can edit the paste. Unglued stores each change as a version with timestamp and optional author, so you can track edits and jump back to earlier revisions.

Under the hood, unglued keeps active pastes **in memory** for maximum speed and stores the version history **gzip-compressed** to save RAM. Pastes expire automatically after a chosen TTL and are garbage-collected, keeping your instance lean.

Because so many workflows start in the terminal, unglued comes with a dead-simple HTTP API: pipe content in, get a URL out—perfect for logs, snippets, or quick notes that expire automatically after a chosen TTL.

### Why unglued

-   **Instant on:** No login or setup: paste in, link out.
-   **Readable by default:** Syntax highlighting, line numbers, and URL-based highlighting for precise pointers.
-   **Collaborative when you want it:** Editable pastes via secret link, with version history and optional author names.
-   **Fast by design:** In-memory store with **gzip-compressed versions** and periodic cleanup.
-   **Terminal-friendly:** Minimal HTTP API for `curl` and friends  
    `cat file.txt | curl -X POST --data-binary @- http://host/api/paste`
-   **Ephemeral, not cluttered:** Automatic expiry keeps data lean and relevant.
-   **Nice touches:** Light/Dark toggle, raw view, and no search engine indexing.

**unglued** — a small, fast way to stick ideas together without getting stuck yourself.

> Note: In-memory means pastes don’t survive process restarts. Need persistence later? Add a disk/DB backend as an optional module.

