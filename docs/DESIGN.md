# Design: the desktop app's visual language

Aotus should feel calm and quiet, like a tool you keep open all day, not like a dashboard. The reference is Grok Bot (`docs/PRD.md`), the product that validated the category. We take its **visual language**: near-black monochrome surfaces, one white primary action, large regular-weight type, thin borders and generous rounded fields. We do **not** take its brand: no logo, mascot, name, icon, illustration or artwork of Grok Bot, xAI or Cursor appears in Aotus. Aotus has its own mark.

Everything lives in `cmd/aotus-desktop/frontend/src/app.css` as CSS tokens. A view never hard-codes a colour, radius or font.

## Rules

1. **Monochrome.** Surfaces go from `--bg` (#0b0b0b) through `--side`, `--surface`, `--surface-2` and `--surface-3`. Text is `--text`, with `--text-2` for secondary and `--text-3` for faint. There is no accent colour.
2. **Colour means status, nothing else.** `--ok` marks a running daemon or session, `--warn` a warning and `--bad` an error, or a destructive action while it is pointed at. A colour never appears for decoration.
3. **One primary action per view.** `button.primary` is white with black text (Link, Create, Allow once, Done). Everything else is `button` (solid dark grey) or `button.ghost` (text only). Destructive actions (`button.danger`) stay grey until hovered.
4. **Type.** System sans (SF Pro, Inter or Segoe UI), 15 px body. Headings are 22 px at weight 500. Never bold walls of text, never uppercase labels. Labels sit above fields in grey at 13 px. Code, paths and commands use `--mono`.
5. **Shape.** Radii `--r-sm` 8, `--r` 12, `--r-lg` 16, `--r-xl` 22 (the composer). Use a 1 px border in `--line` or `--line-strong`, never a drop shadow.
6. **Space.** Pages have 36–40 px of padding and a 760 px reading column. Rows are at least 36 px high. When in doubt, leave more room.
7. **Lists, not tables.** A row is a 44 px tile, a title, a grey description and its actions on the right (`.lrow`, `.tile`). Grok Bot's plugin list is the model.
8. **Say status in words.** The sidebar reads "Working…", "Paused" or the role, not a colour code. A dot only appears when something is happening (white, breathing) or stopped (hollow). The connection dot is green or red.
9. **Conversation.** The user's messages sit in a grey bubble on the right. The employee's answer is plain text on the left with no label. Tool activity is a quiet chip, and a stopped turn is a faint line. The composer is a rounded box with a round white send button.
10. **The CLI's own screen is framed like a window.** A black frame with a thin chrome bar shows what runs and reminds that closing the view leaves it running.

## The Aotus mark

`Mark.svelte` draws two large round eyes. The aotus is the night monkey, known for its huge eyes and for working at night, while you sleep. The mark is white on dark, and its pupils drift slowly in the sidebar. Employees have monochrome initials (`Avatar.svelte`), not generated faces or colours. Icons (`Icon.svelte`) are our own 1.6 px line drawings.

## When adding a view

- Use `.page > .inner`, `h1` and a `.lede`, then `h3.section` headings, `.list` / `.lrow` rows and `.card` forms with `.actions` at the end.
- Use a status colour only for a status.
- Run `npm run check`, `npm test` and `npm run build` in `cmd/aotus-desktop/frontend`, and look at the view in the app.
