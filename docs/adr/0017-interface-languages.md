# ADR 0017 — Interface languages (Spanish source, English)

- Status: Accepted
- Context: the web desktop, TangoConnect and every message from tangod and tango-agent were written in Spanish.
  The interface has to be available in English without rewriting the backend or the protocol, and without making
  the Spanish source harder to read.
- Decision:
  1. **The Spanish text is the key.** `t("Crear volumen")` (`@tango/client`, `web/I18N.md`) returns the English
     translation when the interface is in English and the same text otherwise. Variables are `{name}` placeholders,
     plurals go through `tn()`, and the rare word with two meanings takes a context (`t("Grupo", vars,
     "almacenamiento")`, catalog key `almacenamiento|Grupo`).
  2. **One catalog per package** (`src/i18n.en.ts`, imported first by the package entry) registered into one map.
     A test loads every catalog and fails if the same key has two different translations.
  3. **Messages from the NAS are translated in the browser.** `scripts/i18n-extract` collects the Spanish strings and
     Go format strings that tangod and the agent can send (errors, notices, job titles and progress) into
     `server.es.json`; `server.en.ts` translates each one keeping the Go verbs. `tr()` matches an incoming message
     exactly or against those formats (nested "context: detail" messages included). `RpcError` translates its message
     when it is created; notices and jobs are translated where they are shown. `make check` fails if the list is stale
     and a web test fails if a string has no translation. The protocol does not change and an older NAS still works
     with a newer desktop (untranslated messages stay in Spanish).
  4. **Language choice per browser**: saved in `localStorage` (`tango.lang`), default from the browser language
     (Spanish for `es-*`, English otherwise), switchable on the login screen, in Settings → Appearance and in the
     TangoConnect footer. Changing it reloads the page, so module-level labels can use `t()`. Dates and numbers use
     `locale()`.
  5. Data is never translated (names of volumes, folders, users, files; logs; confirmation words the NAS compares,
     such as `REINICIAR`).
- Consequences: adding a language is a set of catalogs plus an entry in `LANGS`. Unit tests and Playwright run in
  Spanish (`locale: es-AR`). The installer has its own small dictionaries (Spanish, English, Portuguese) because it is
  a separate dependency-free page.
