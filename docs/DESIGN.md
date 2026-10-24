# Design: the desktop app's visual language

Aotus should feel calm and quiet, like a tool you keep open all day, not like a dashboard. Its identity is **night amber**, taken from the aotus, the night monkey: warm ink-black surfaces and the amber of its large eyes.

Grok Bot (`docs/PRD.md`), the product that validated the category, is an **inspiration, not a model to copy**. From it we keep the restraint: dark surfaces, one clear primary action per view, large regular-weight type, thin borders, rows rather than tables. What makes Aotus its own is the warm ink instead of neutral grey, amber instead of white for action and activity, the blinking eyes, the lowercase wordmark and its words (teammates, a soul). There is **no logo**, mascot, name, icon, illustration or artwork of Grok Bot, xAI or Cursor anywhere in Aotus.

Everything lives in `cmd/aotus-desktop/frontend/src/app.css` as CSS tokens. A view never hard-codes a colour, radius or font.

## Rules

1. **Warm ink, otherwise monochrome.** Surfaces go from `--bg` (#0e0c0a) through `--side`, `--surface`, `--surface-2` and `--surface-3`, all a little warm. Text is `--text` (warm white), with `--text-2` for secondary and `--text-3` for faint.
2. **Amber means action and activity.** `--amber` (#f2a93b) is the one primary action of a view, work in progress (the breathing dot, the streaming cursor), focus, the active tab and a switch that is on. Nothing else is amber. Green (`--ok`) and red (`--bad`) only say status: a running daemon, an error, a destructive action while it is pointed at. No colour is decoration.
3. **One primary action per view.** `button.primary` is amber with ink text (Link, Set up my teammate, Hire, Allow once, Done). Everything else is `button` (solid dark warm grey) or `button.ghost` (text only). Destructive actions (`button.danger`) stay grey until hovered.
4. **Type.** System sans (SF Pro, Inter or Segoe UI), 15 px body. Headings are 22 px at weight 500. Never bold walls of text, never uppercase labels. Labels sit above fields in grey at 13 px. Code, paths and commands use `--mono`.
5. **Shape.** Radii `--r-sm` 8, `--r` 12, `--r-lg` 16, `--r-xl` 22 (the composer). Use a 1 px border in `--line` or `--line-strong`, never a drop shadow.
6. **Space.** Pages have 36–40 px of padding and a 760 px reading column. Rows are at least 36 px high. When in doubt, leave more room.
7. **Lists, not tables.** A row is a 44 px tile, a title, a grey description and its actions on the right (`.lrow`, `.tile`).
8. **Say status in words.** The sidebar reads "Working…", "Paused" or the role, not a colour code. A dot only appears when something is happening (amber, breathing) or stopped (hollow). The connection dot is green or red.
9. **Conversation.** The person's messages sit in a warm grey bubble on the right. The teammate's answer is plain text on the left with no label. Tool activity is a quiet chip, and a stopped turn is a faint line. The composer is a rounded box with a round amber send button, and the streaming cursor is amber.
10. **The CLI's own screen is framed like a window.** A warm black frame with a thin chrome bar shows what runs and reminds that closing the view leaves it running.

## The Aotus mark and wordmark

`Mark.svelte` draws the night monkey's two large amber eyes: the aotus works at night, while you sleep, which is what teammates do. The eyes blink about every six seconds and their pupils drift slowly in the sidebar. Motion stops for people who ask for reduced motion. The wordmark is `aotus` in lowercase, set tight and a little heavy (`.wordmark`). Teammates have warm monochrome initials (`Avatar.svelte`), not generated faces or colours. Icons (`Icon.svelte`) are our own 1.6 px line drawings. The tray icon is an amber disc.

## When adding a view

- Use `.page > .inner`, `h1` and a `.lede`, then `h3.section` headings, `.list` / `.lrow` rows and `.card` forms with `.actions` at the end.
- Use a status colour only for a status.
- Run `npm run check`, `npm test` and `npm run build` in `cmd/aotus-desktop/frontend`, and look at the view in the app.
