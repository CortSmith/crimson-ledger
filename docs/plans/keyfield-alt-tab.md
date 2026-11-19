# Plan: alt-tab must not change input fields

Applies to both apps (crimson-ledger, crimson-scout) via the shared `internal/ui/page/keyfield.js`.

## Cause
1. `window` `blur` (fires on alt-tab) calls `remask()`, which overwrites the input with the mask and clears `edited`. A key being typed/pasted is lost.
2. The 15 s auto re-mask timer does the same mid-typing.
3. The input `focus` handler clears the mask placeholder. On alt-tab back, the browser re-focuses the field, so an untouched field is blanked.
No other input in either app has blur/focus/visibility handlers (verified).

## Change
- Add `hide()`: for passive triggers (window blur, 15 s timer). Cancels the timer, sets `type=password`, icon to "show". If the user has typed (`edited`), the value is kept; otherwise it falls back to `remask()` (a revealed, unedited key goes back to the mask, so the secret still leaves the DOM).
- Escape and the eye button stay an explicit `remask()` (user intent).
- Replace the focus-clears-mask behaviour with: `focus` selects the text (no value change); `beforeinput` clears the mask the first time the user actually types/pastes/deletes.

## Tests (`keyfield.test.mjs`)
- Blur with typed text keeps the text and hides it; blur on revealed unedited key re-masks.
- Timer behaves the same; Escape still fully re-masks.
- Focus alone never changes the value; first beforeinput clears the mask.

## Docs
Update T12 line 65 wording (blur/timeout hide, never discard typed input).
