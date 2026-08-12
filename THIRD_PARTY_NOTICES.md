# Third-party notices

## Stalwart Mail Server

MEOVV Mail distributes the unmodified Stalwart Mail Server as a separate container:

- Project: Stalwart Mail Server
- Version: `v0.16.17`
- Image: `stalwartlabs/stalwart:v0.16.17`
- Digest: `sha256:a8108e19bd927e172d4d8c128907b8dfc93fd180ae8ee07dccdd42cb97eb9dfa`
- License: GNU Affero General Public License, version 3
- Corresponding source: https://github.com/stalwartlabs/stalwart/tree/v0.16.17
- Release: https://github.com/stalwartlabs/stalwart/releases/tag/v0.16.17

Stalwart is not linked into the MEOVV application. MEOVV communicates with it over documented HTTP/JMAP and SMTP interfaces and ships it unmodified as its own service. Any future modifications to Stalwart must be published with corresponding source under the applicable license. Obtain legal review before customer distribution.

Other JavaScript and Go dependencies retain their respective upstream licenses.
Their exact versions are recorded in `package-lock.json` and `go.sum`. Every
published binary or appliance release must include a generated software bill of
materials and the applicable third-party license texts; this source repository
does not replace those release artifacts.
