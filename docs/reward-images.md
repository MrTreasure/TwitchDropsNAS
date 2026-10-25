# Repeatable reward image ingestion

## Automatic path

Normal campaign synchronization reads `benefitEdges[].benefit.imageAssetURL` and
keeps approved original CDN HTTPS URLs for every game. No game-specific URL table
or image download is required. Multiple benefits retain their own thumbnails.
Images are presentation metadata; they never enter goal completion or watch-time
calculation. A readable picture does not imply readable account progress.

## Browser-page fallback

If Twitch does not return images through the current authorization, open the
ordinary Twitch campaign page and expand the relevant activity. In the NAS UI,
choose **奖励图片**, select the activity, and expand **如何从 Twitch 页面批量提取**.
Review and copy its extraction script, then run it in the Twitch page's developer
console. It reads reward image names, original URLs and nearby labels, makes no
network request and reads no cookies or credentials. Its `copy(...)` result is in
the clipboard; the console may display `undefined`.

Paste the exported JSON into the NAS dialog. A reward-region HTML fragment is
also accepted: the backend only parses image attributes and nearby text, never
executes or renders the supplied HTML. Input is limited to 16 KiB text / 200
entries within the existing 32 KiB request boundary. Extract only the activity
you need if the page is too large.

## Review and save

- Select an exact `rewardImageScopes[].id`; no cross-game or cross-activity match.
- Match exact normalized reward names or registered aliases. Empty image `alt`
  can use a unique nearby label from the exported context or HTML container.
- Names that identify multiple rewards remain unmatched. Never choose by order,
  similarity, image appearance, or the first UUID.
- Unrelated entries remain unmatched and are not saved. Invalid CDN addresses
  and multiple different URLs for one reward block saving until corrected.
- Correct an unmatched name in the JSON to the displayed reward name and preview
  again. API clients can use a `rewardKey` returned by a previous matching plan.
- Confirm the preview to persist only matched original URLs, source type and
  import time in the existing SQLite volume. The raw input, HTML, context text,
  image bodies and unmatched records are not stored.
- Changes take effect immediately and survive restart. No source edits or
  deployment are needed. Fresh Twitch images take precedence over imported
  fallback images; explicit public-notice imports override bundled illustrations.

## API / agent integration

Use the authenticated API or trusted host Unix adapter; never the Twitch token
as a management credential. Read the current scope list from `/snapshot`.

```json
{
  "scope": "announcement:EXACT_ID_FROM_SNAPSHOT",
  "input": "[{\"name\":\"Exact reward name\",\"imageUrl\":\"https://static-cdn.jtvnw.net/twitch-quests-assets/REWARD/PUBLIC_IMAGE_ID.png\"}]"
}
```

1. `POST /reward-images/preview` returns `matches`, `unmatched`, `invalid`,
   `conflicts`, and `canApply`.
2. Show the mapping for review. If a human initiated the import and confirmed
   the mapping, send the same body to `POST /reward-images/import`.
3. Import recomputes against the current activity, rejects unresolved conflicts,
   and returns `updated`. Read `/snapshot` to confirm the resolved image URLs.

Only configured public CDN hosts and HTTPS without credentials are accepted.
Adding another image CDN requires aligned backend, UI and host CSP allowlists.
There is no server-side fetch or image proxy. Validation stops at metadata; a
missing image is shown as unavailable by the browser, not replaced with a fake.
