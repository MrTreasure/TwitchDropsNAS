# Upstream references

Twitch metadata fields, public client identifiers, persisted-operation identifiers
and watch-event interoperability were checked against:

- https://github.com/DevilXD/TwitchDropsMiner (MIT, Copyright (c) 2024 DevilXD)
- https://github.com/1em0ntea/TwitchDropsMiner-Headless (MIT)
- https://github.com/vocino/TwitchDropsMiner-CLI (MIT)

This is an independent service. Its low-traffic playback path follows the current
DevilXD channel.py implementation: read HLS playlist metadata and issue segment
HEAD requests without downloading video/audio bodies. It has no browser, decoder,
automatic claim implementation or integrity-challenge workaround.

## War Thunder artwork

The optional War Thunder UI style links directly to original logo and GUI icons
hosted by War Thunder / Gaijin. Game trademarks and artwork belong to their
respective owners and are not covered by this project's MIT license. Original game artwork is not redistributed as standalone assets; documentation
screenshots may include the artwork as rendered by the interface. This project
is an unofficial, independent tool and is not endorsed by Twitch or Gaijin.

Official asset sources: https://warthunder.com/assets/img/svg/logo-wt.svg,
https://warthunder.com/i/favicons/favicon.ico,
https://static.encyclopedia.warthunder.com/gui_skin/item_type_rp.svg,
https://static.encyclopedia.warthunder.com/gui_skin/item_type_talisman.svg.

## MIT License (upstream reference)

Copyright (c) 2024 DevilXD

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Public campaign discovery

The optional runtime fallback uses public discovery metadata from
https://twitch-drops-api.sunkwi.com/v2/drops. No catalogue snapshots or user data
are distributed in this source tree or release archives. This service is not
Twitch; its data cannot prove account linkage, progress, eligibility or claims.
The compatibility limitation and fallback approaches were compared with the
unmerged upstream proposals https://github.com/DevilXD/TwitchDropsMiner/pull/1174
and https://github.com/DevilXD/TwitchDropsMiner/pull/1180. This implementation
keeps official confirmation and anonymous public discovery separate.
