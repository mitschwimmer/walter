# Walter: clear and curious

Walter is an independent product for primary-school children, teenagers and their parents. Its visual language is curious and approachable without infantilizing the audience. No shared Heritage stylesheet or design tokens.

## Design charter

- Keep the original horse signet and the exact layered headline effect: the existing rainbow stops, theme-specific colors, geometric SVG overlay and text clipping. Do not recolor the SVG or invent a new brand gradient. Use the signet consistently in the masthead and assistant replies.
- Clear, cool neutral surfaces in light mode, deep blue-violet surfaces in dark mode, bold sans-serif display typography and softer rounded controls. Reserve the rainbow for brand identity; use calm blue and violet for equal-weight Schnell and Bedacht actions. No decorative background fields, strong separators, gamification or childish illustration.
- Use the existing native Oat CSS subset for technical control basics; all Walter product tokens and overrides belong in `web/style.css`. Do not reintroduce Archaic Heritage. Theme defaults to the OS, with an explicit System / Hell / Dunkel choice saved locally and restored before CSS. Use the same theme tokens for dialogs, errors and Markdown.
- A single responsive central column. The welcome screen centers the headline, short invitation and unified composer. Starter questions fill only an empty draft; they never send automatically. Keep both thinking modes explicit and visible, with accessible labels and large touch targets.
- In the conversation, show a quiet user bubble and a clearly identified Walter reply; keep the answer readable, with narrow desktop paragraphs, no decorative frame around the response, horizontally scrollable code and tables, and an accessible reduced-motion loading state. The answer composer stays easy to reach. Keep the existing context, cancellation, retry and persistence behavior.
- History uses soft individual conversation rows, title first and date second. Keep direct delete actions recognizable and secondary, with the existing confirmation. Maintain login, logout and keyboard navigation. Provide visible focus, forced-color fallbacks and no horizontal page overflow at narrow widths.
- Preserve the original content/security boundaries: no inference or identity credentials in frontend assets, no client-side rendering of unrestricted HTML, and no new frontend build system.

## Visual review

Review light and dark, desktop and narrow mobile, including a complete Markdown reply. Keep existing scenario IDs stable. See [testing](testing.md). The design is based on the selected Clear & Curious prototype, not on completed user research; validate with children and parents before claiming accessibility or age suitability.
