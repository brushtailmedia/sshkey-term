# Cryptographic Correctness Audit — sshkey-term (client)

Code-level review of the term client's end-to-end cryptography and, crucially,
its **enforcement**. Date: 2026-05-30.

> **Status: complete (2026-05-30).** 12 findings (F1–F12): **four HIGH**
> (F1, F6, F7, F12), one MEDIUM (F11), the rest LOW/INFO. The cryptographic
> primitives are sound; the gaps are **missing receive-side verification** plus
> one **path-traversal** in the file-cache write (F12). See the Bottom line and
> Recommended priority sections. Code-level review, not a professional audit.

## Scope & threat model

- **In scope:** `internal/crypto` primitives and their call sites in
  `internal/client` (send + receive), key pinning / TOFU, safety numbers,
  epoch keys, attachments, at-rest key storage.
- **Out of scope (noted, NOT audited):** the server (`sshkey-chat`) crypto, the
  full wire-protocol spec, the SSH transport config, side-channel/timing
  analysis, and formal/mathematical proofs.
- **Threat model** (the codebase's own stance): the **server is untrusted** for
  message content and authorship — it is authoritative only for ordering and
  routing metadata. E2E guarantees must hold against a malicious server, and in
  multi-party rooms/groups, against malicious co-members who hold the shared
  key.
- **This is a code-level review by a non-specialist, not a professional audit.**
  Commission one before adversarial use.

## Findings summary

| ID | Severity | Title |
|----|----------|-------|
| F1 | **HIGH** | Normal messages signed on send but never verified on receive (impersonation in rooms/group-DMs) |
| F6 | **HIGH** | Deletes / reactions / un-reacts / pins are not authenticated on receipt (server can forge + misattribute) |
| F7 | **HIGH** (design-dependent) | Room epoch-key recipients are the server's trigger list; `MemberHash` never verified → a malicious server can add an eavesdropper to a room |
| F12 | **HIGH** | Path traversal via unsanitized `fileID` in the download cache write → arbitrary-path file write |
| F11 | MEDIUM | File-download integrity uses a server-provided hash; room files are substitutable |
| F2 | LOW–MED | Send-path signatures lack length-prefixing / domain separation |
| F8 | INFO (design) | No forward secrecy / post-compromise security — identity-key compromise decrypts all past + future |
| F9 | LOW | Room content reuses the epoch key with random 96-bit nonces (~2³² birthday bound per epoch) |
| F3 | LOW | `SafetyNumber` is entropy-lossy and short (24 digits) |
| F4 | LOW | TOFU pin overwritten to the changed key before the user decides |
| F10 | INFO | Unencrypted key-generation branch exists (gated by the mandatory-passphrase UI) |
| F5 | INFO | `Encrypt`/`Decrypt` don't assert a 256-bit key |

## Bottom line

The cryptographic **primitives are implemented correctly** — sound AEAD / ECIES
/ Ed25519 / HKDF construction, `crypto/rand` everywhere (zero `math/rand` in the
tree), correct Ed25519↔X25519 conversions, passphrase-encrypted keys at `0600`,
whole-DB SQLCipher at rest, no hardcoded secrets. **The gap is enforcement on
the receive side: the client verifies almost nothing it receives.** Only message
**edits** and file **content-hashes** are verified; **normal messages, deletes,
reactions, un-reacts, and pins are accepted on the server's word** — the actor is
read from a server-authored envelope field with no signature check (F1, F6). And
**room** key distribution trusts the server's membership list with no
`MemberHash` verification (F7). Against the project's own "untrusted server"
threat model, that currently means:

- **DM / group-DM content confidentiality vs the server: holds** — per-message
  keys are wrapped only for the chosen peers; the server never sees them.
- **Room content confidentiality vs an *active* server: does NOT hold** — the
  server can list itself / a colluder as an epoch-key recipient (F7).
- **Message & action authenticity vs the server: does NOT hold** for normal
  messages, deletes, reactions, pins (F1, F6) — only *edits* are authenticated.
- **No forward secrecy** (F8): identity-key compromise → all history + future.
- **File handling vs the server:** room-file *content* can be substituted (F11),
  and an unsanitized `fileID` lets a malicious server write decrypted content to
  an **arbitrary path** on the recipient's disk (F12 — path traversal, RCE-class).

The encouraging part: **every gap is a missing verification call, not a broken
primitive** — and the **edit path already demonstrates the correct
verify-or-drop + domain-separation + msgID-binding pattern.** Extending that to
the send-path message handlers and the delete/react/pin handlers, plus a
`MemberHash` check on epoch-key receipt, closes F1/F6/F7. These are most likely
**pre-launch deferrals** (the app has no users yet), but they must land before
the E2E claims hold against a real adversary.

---

## F1 — Normal messages signed on send, never verified on receive (HIGH)

**Evidence.** The send path signs every message — `crypto.SignRoom` /
`crypto.SignDM` (`internal/client/send.go:99,240,494,541,823,965`) — and ships
the signature. The receive path does **not** verify it. Repo-wide call counts:

```
crypto.Sign*  : SignDM 6, SignRoom 3, SignDMEdit 4, SignRoomEdit 2   (15 signs)
crypto.Verify*: VerifyDMEdit 2, VerifyRoomEdit 2, VerifyContentHash 1
                VerifyDM 0,  VerifyRoom 0                            (edits only)
```

`storeRoomMessage` / `storeGroupMessage` / `storeDMMessage`
(`internal/client/persist.go:64,153,209`) only **decrypt** (`DecryptRoomMessage`
etc.) and run a replay check (`checkReplay`, persist.go:85). `msg.From` is taken
straight from the server-relayed envelope and used as the author. `VerifyRoom(`
/ `VerifyDM(` have **zero call sites in the entire repository.** Only *edits* are
verified-or-dropped (persist.go:391,441,486, against the pinned key via
`pubKeyForUser`).

**Impact.** The Ed25519 signature is the *only* mechanism binding a message to
its author; unverified, sender authentication is not enforced for normal
messages:
- **Rooms / group DMs** (epoch/symmetric key shared by all N members):
  decryption success no longer implies "from the claimed sender" — any holder
  of the shared key can produce a decryptable message, and a malicious server
  can attribute any decryptable ciphertext to any sender. → **sender
  impersonation / forged attribution** in multi-party contexts.
- **1:1 DMs** are less exposed (2-party; key-possession narrows authorship to
  "the other party"), but **group DMs are fully exposed.**
- This is **not** a replay issue (`checkReplay` exists); it is
  authentication-of-author.

**Server-side refinement (per the server audit, `sshkey-chat/docs/security/crypto_audit.md`
§S1).** The relay is *actor-authoritative*: it stamps `from` from the
authenticated SSH session (`c.UserID`), and the inbound envelope has **no
client-settable sender field**. So a co-member who forges a decryptable
ciphertext has it attributed to **themselves**, not the victim — *member-level*
impersonation is blocked by an honest server. The exploit therefore requires a
**malicious / compromised server** (which controls the `from` field and which
this client, by never verifying, cannot catch). That is squarely inside the E2E
threat model — not trusting the server is the whole point — so **F1 stands as
HIGH**; the correction is only that the practical actor is the *server*, not an
ordinary co-member.

**Why it stands out.** The signing infrastructure exists, is exercised on send,
and the *edit* path is properly verify-or-drop — the normal receive path simply
never calls `Verify`. This reads as a deferred/overlooked step, not a primitive
flaw.

**Recommendation.** On every inbound room/group/DM message, verify
`SignRoom`/`SignDM` against the sender's **pinned** key and **drop on failure**,
mirroring the edit path's verify-or-drop. Confirm whether the omission is an
intentional pre-launch deferral; if so, track it explicitly — it is the single
finding that most changes the security story.

## F2 — Send-path canonical signing forms lack length-prefixing / domain separation (LOW–MED)

`SignRoom = payload ‖ room ‖ epoch` and
`SignDM = payload ‖ conversation ‖ wrappedKeysCanonical`
(`internal/crypto/crypto.go:188,209`) concatenate adjacent **variable-length**
fields with no delimiters, no length prefixes, and no domain tag. Consequences:
- **Boundary ambiguity:** different field splits can yield identical signed
  bytes (`"AB"‖"CDE"` vs `"ABC"‖"DE"`), enabling signature *reinterpretation*
  given a valid signature.
- **Cross-context confusion:** a `SignRoom` signature and a `SignDM` signature
  can share a byte-string and cross-verify, because nothing domain-separates
  them.

The team already fixed exactly this for **edits**: `SignRoomEdit` /
`SignDMEdit` (crypto.go:244,268) prepend a domain tag (`"edit_room:"` /
`"edit_dm:"`) and a **length-prefixed** `msgID` — with a comment describing the
substitution attack. The base send-path forms never got the same treatment.
Largely academic while **F1** stands (nothing is verified), but when F1 is
closed, the send forms must use the hardened canonical shape. (`MemberHash`
concatenates usernames with no delimiter too — same pattern; impact bounded by
the nanoid username format.)

**Recommendation.** Adopt domain tags + length-prefixed variable fields for
`SignRoom`/`SignDM` (and `wrappedKeysCanonical` entries), as the edit forms do.

## F3 — `SafetyNumber` is entropy-lossy and short (LOW)

`SafetyNumber` (crypto.go:334) emits **24 decimal digits** (~80 bits) derived
from only **24 of the 32** SHA-256 bytes, via biased `byte % 100` reductions and
a `sum % 100` collapse (`int(hash[i])%100 + int(hash[i+12])%100`, then `% 100`,
for i = 0..11). It is not broken (≈2⁸⁰ work for a colliding MITM key), but it
discards entropy for no reason and is short next to comparable designs (Signal
uses 60 digits). **Recommendation:** encode the full 32-byte hash uniformly
(e.g. modular-reduce a big-integer view, or bytes→digits without `%100` bias)
and/or use more digits.

## F4 — TOFU pin overwritten to the changed key before the user decides (LOW)

On a detected key change, `StoreProfile` (persist.go:665) warns
(`OnKeyWarning`, which is **async** — pushes to a channel and returns) and
`ClearVerified`, then **unconditionally** `PinKey(new)` (persist.go:689). The
trust anchor therefore moves to the new (possibly attacker) key *immediately*,
ahead of the Accept/Disconnect decision — and because edit-verify resolves the
key via the pin (`pubKeyForUser`), the attacker's *edits* would then verify. A
stricter TOFU keeps the **old** pin until explicit acceptance. Mitigated by the
loud blocking modal + `ClearVerified` + the "no legitimate rotation" invariant
(any change is anomalous), but the pin-before-decision weakens the warning's
value. **Recommendation:** do not overwrite the pin on a detected change until
the user accepts.

## F5 — `Encrypt`/`Decrypt` don't assert a 256-bit key (INFO)

`aes.NewCipher` accepts 16/24/32-byte keys, so a 16-byte key would silently
become AES-128. All keys are 32 bytes today (`GenerateKey`, HKDF output), but an
explicit `len(key) == 32` guard would prevent a future downgrade-by-bug.

## F6 — Deletes / reactions / un-reacts / pins are not authenticated on receipt (HIGH)

Generalizes F1 to message-mutation and metadata actions. None is verified on
receipt, and the **actor is read from a server-authored envelope field**:

| Action | Signed on send? | Verified on receive? | Actor source |
|--------|-----------------|----------------------|--------------|
| Delete / tombstone | **No** — `protocol.Delete` has no sig field (`send.go:668`) | **No** (`client.go` `case "deleted"`; `persist.go:644` `storeCatchupTombstone`) | `Deleted.DeletedBy` (server) |
| Reaction **add** | Yes — `SignRoom`/`SignDM` over the encrypted payload (`send.go:494,541,965`); does **not** bind actor or target msgID | **No** — `storeReaction` never calls Verify, ignores `r.Signature` | `Reaction.User` (server) |
| Reaction **remove** | **No** — `protocol.Unreact` has no sig (`send.go:561`) | **No** (`client.go` `case "reaction_removed"`) | server |
| **Pin / Unpin** | **No** — `protocol.Pin`/`Unpin` have no sig (`app.go:3206-3216`) | **No** (`app.go:7738,7773,7782`) | `Pinned.PinnedBy` (server) |

A malicious relaying server can therefore **forge a delete, reaction, or pin and
attribute it to any user**, and **suppress** messages/reactions at will
(`reaction_removed`, tombstone). The reaction *send* path is the only one that
signs — but the signature is never verified and wouldn't bind the actor anyway.
(There is a post-decrypt plaintext check `dr.Target != r.ID` for reactions,
flagged "server tampering" — a consistency check, not authentication.)
**Recommendation:** actor-binding signatures (msgID + actor in the canonical
form) + verify-or-drop on receipt, consistent with the edit path.

> **Server-side note (server audit §S1).** As with F1, the relay stamps
> `DeletedBy` / `PinnedBy` / reaction-`User` from the authenticated session and
> re-authorizes edits/deletes/unreacts against the *stored* sender — so a
> *co-member* cannot forge one of these attributed to another user. The forgery
> requires a **malicious server**, which is exactly the "Actor source (server)"
> column above. F6 stands as HIGH on that basis.

## F7 — Room epoch-key recipients are server-supplied; `MemberHash` never verified (HIGH, design-dependent)

`handleEpochTrigger` (`epoch.go:23`) wraps the new room epoch key for
**`trigger.Members`** — the recipient list (usernames + pubkeys) supplied by the
server's `epoch_trigger` — with **no cross-check** against the rotating client's
own view of room membership:

```go
for _, member := range trigger.Members {
    pubKey, _ := crypto.ParseSSHPubKey(member.PubKey)
    wrapped, _ := crypto.WrapKey(epochKey, pubKey)   // wraps for whoever the server listed
    wrappedKeys[member.User] = wrapped
}
```

The client computes `crypto.MemberHash` over that same server-supplied set and
**sends** it (`epoch.go:54,62`), but **no receive path verifies it** — repo-wide,
`MemberHash` has send-side references only; `EpochConfirmed` has no hash field;
receivers `UnwrapKey` and cache without checking membership.

Consequence: a malicious server can **list itself or a colluder in
`epoch_trigger`**, causing a legitimate member to wrap the room key for the
attacker — who can then **decrypt every room message in that epoch**. Room
content is therefore **not E2E-confidential against an active malicious server.**

**Design caveat:** rooms are operator-managed (rooms / groups / DMs use
deliberately different control models), so "the server/operator defines room
membership" may be an *intended* trust boundary — in which case this is a
documentation matter, not a vuln. But a `MemberHash` "for epoch rotation
verification" that is computed and transmitted yet **never checked** strongly
suggests client-side verification was intended and not wired up — the same
sign-but-don't-verify shape as F1/F6. **Recommendation:** verify `MemberHash`
against an independently-trusted member list on epoch-key receipt (reject
recipients the client doesn't believe are members); at minimum, document rooms
as server-trusted for membership if that is the intent. DMs/group-DMs don't
share this gap — the sender wraps per-message keys for peers *it* selects.

> **Server-side note (server audit §S1 / membership review).** The *honest*
> server builds the `epoch_trigger` recipient list from its own `room_members`
> table joined to each member's **bound** `users.key` (`epoch.go:300-309`), never
> from a client-supplied value, and no network user can self-join a room. So an
> honest server lists exactly the real members — the eavesdropper injection
> requires a **malicious server**, confirming the "design-dependent" framing: the
> client's missing `MemberHash` check is what makes that injection *undetectable*.

## F8 — No forward secrecy / post-compromise security (INFO — design property)

The long-lived Ed25519 identity key unwraps **all** per-message keys (DMs/group
DMs) and decrypts **all** persisted epoch keys (rooms — old epoch keys are kept
indefinitely, in memory and in the SQLite `epoch_keys` table, to decrypt
history; no eviction on rotation, no key zeroization). No ratchet exists.
**Compromise of the identity private key ⇒ decryption of all past and future
messages** the user can see. A deliberate trade (history must survive
restart/sync), but state it plainly: the model gives confidentiality vs the
server + at-rest, **not** forward secrecy or post-compromise security.

## F9 — Room content reuses the epoch key with random 96-bit nonces (LOW)

`crypto.Encrypt` uses a random 96-bit GCM nonce — safe only up to the birthday
bound (~2³² encryptions per key). **Room** messages, reactions, edits, and file
uploads all encrypt under the **same long-lived epoch key** (`send.go:90,487`,
`edit.go:83`, `filetransfer.go:111` via `UploadFile`), so all room activity in an
epoch accumulates random nonces under one key. A collision (≈50% near 2³²) would
leak a plaintext-XOR and the GCM auth key. Practically unreachable — epochs
rotate on every membership change and rooms never approach billions of messages
per epoch — but a counter-based nonce (or a documented per-epoch cap) would
remove the bound. **DMs/group-DMs are immune** (fresh per-message key → one nonce
per key).

## F10 — Unencrypted key-generation branch exists, gated by the UI (INFO)

`generateEd25519KeyFile` (`internal/tui/keygen.go:37-41`) writes an
**unencrypted** OpenSSH key when `passphrase == ""` (else
`MarshalPrivateKeyWithPassphrase`, bcrypt-pbkdf). The wizard / add-server flows
gate this with mandatory passphrase validation (`internal/keygen/strength.go`:
12-char min + zxcvbn floor; "generating an unencrypted key is unsafe"), so it's
unreachable through the UI — but the generator itself would write plaintext if a
future caller passed `""`. **Recommendation:** hard-fail on empty passphrase
inside `generateEd25519KeyFile` (defense in depth).

## F11 — File-download integrity check uses a server-provided hash; room files are substitutable (MEDIUM)

`DownloadFile` runs `crypto.VerifyContentHash(data, expectedHash)`
(`filetransfer.go:406`) where `expectedHash = pending.contentHash` is set from
the **server's `download_start` frame** (`HandleDownloadStart(fileID,
contentHash)`, `filetransfer.go:296-300`) — **not** from the E2E-encrypted
message. The server controls both the blob and its expected hash, so the check
gives **no integrity against a malicious server** (the code comment honestly
scopes it to "truncation, bit rot, transit corruption").

Real integrity comes from the GCM tag under the **E2E `decryptKey`** (delivered
inside the encrypted message payload). That protects **group/DM files** — each
has a unique per-file key, so a substituted blob fails to decrypt. But **room
files share the epoch key**, so a malicious server can return *another
same-room/same-epoch file's* ciphertext for a requested `fileID`: it decrypts
cleanly (same key) and the server supplies a matching content hash → the swap is
**undetected**. The E2E payload commits to `FileID` but **not** to a content
hash, so nothing client-side binds `FileID → content`. **Recommendation:** put
`ContentHash(ciphertext)` inside the E2E attachment metadata and verify against
*that* (or bind `FileID` into the file-AEAD additional data).

## F12 — Path traversal via unsanitized `fileID` in the download write path (HIGH)

On a successful download the plaintext is written to
`filepath.Join(filesDir, fileID)` (`filetransfer.go:417`; the upload cache uses
`config.AttachmentPath(dataDir, fileID)` at `:200`) with **no validation of
`fileID`** — no nanoid-format check, no rejection of `/` or `..` (repo-wide, the
only `ValidateNanoID` is a *comment* about the server). `fileID` is
attacker-influenceable: it is written by the sender into the **E2E attachment
metadata** (`payload.Attachments[].FileID`) and used verbatim. A `fileID` like
`../../../.config/autostart/x.desktop` resolves out of `filesDir`, so a
successful download **writes decrypted content to an arbitrary path** (mode
`0600`).

Reachability (active-server threat model): the write at `:417-418` runs only if
the download succeeds — i.e. the server returns a blob for the attacker-chosen
`fileID` that decrypts under the recipient's key. A malicious server can serve a
legitimate ciphertext (any file the recipient can decrypt; with a colluding
sender, fully attacker-chosen content) for the traversal `fileID`. **Auto-preview
makes it low-interaction** — image attachments auto-download (`DownloadFile` on
the preview path, `app.go:3303,3349`; also `o`/save at `:3255`). Net: a malicious
server can write (semi-)controlled content to an arbitrary path on the
recipient's machine → persistence / potential RCE.

**Asymmetry to note:** the user-facing *Save-As* path **is** correctly sanitized
(`sanitizeAttachmentName` → `filepath.Base` + reject `.`/`..`/`\x00`/`/`/`\`,
`internal/tui/saveattachment.go:336`); it's the internal *cache* write that is
not. **Recommendation:** validate `fileID` against the nanoid format (or
`filepath.Base` + reject separators) before any `filepath.Join`, at both
`filetransfer.go:417` and the upload-cache path.

---

## Verified-correct (no issue found)

- **Randomness:** `crypto/rand` throughout `crypto.go` (no `math/rand`); nonces,
  keys, and ephemeral scalars all from `rand.Read`.
- **X25519:** `curve25519.X25519` errors are checked in both `WrapKey` and
  `UnwrapKey`, rejecting low-order points / all-zero shared secrets.
- **Ed25519 → X25519:** correct derivation — private scalar = `SHA-512(seed)`
  clamped (crypto.go:403); public via `edwards25519.Point.SetBytes` (on-curve
  validated) → `BytesMontgomery` (crypto.go:393).
- **Key wrap:** ECIES with ephemeral-static X25519 + HKDF-SHA256 (binding
  `ephPub` as salt) + AES-256-GCM; the wrapping key is **fresh single-use** per
  wrap (unique ephemeral ECDH), so the wrap AEAD cannot reuse a (key, nonce).
- **Edit path:** properly hardened — domain tags, length-prefixed msgID,
  verify-or-drop against the pinned key.
- **TOFU + host keys:** key pinning with change detection + blocking modal;
  SSH host-key pinning (`hostkey.go`).
- **File integrity:** BLAKE2b-256 content hash over the *encrypted* bytes.
- **Randomness (whole-tree sweep):** `crypto/rand` everywhere; **`math/rand`
  appears nowhere in `internal/`** (test or non-test). Keys, nonces, ephemeral
  scalars, device IDs, corr-IDs, and nanoids all use `crypto/rand` (with
  `crypto/rand.Int` rejection sampling for unbiased IDs).
- **Key generation:** Ed25519 via `ed25519.GenerateKey(crypto/rand.Reader)`
  (`internal/tui/keygen.go:31`); no hardcoded keys / fixed seeds / `NewKeyFromSeed`
  in non-test code.
- **Private key at rest:** OpenSSH format, passphrase-encrypted via
  `ssh.MarshalPrivateKeyWithPassphrase` (bcrypt-pbkdf), mode `0600` (parent dirs
  `0700`); decrypted **in memory only** — never written back to disk in plaintext.
  (See F10 for the gated empty-pass branch.)
- **DB at rest:** whole-DB SQLCipher, keyed by `DeriveDBKey` = HKDF-SHA256 of the
  Ed25519 seed (info `"sshkey-chat local db"`); `Open` refuses an empty key.
- **DM / group-DM content confidentiality vs the server:** per-message keys are
  freshly generated and wrapped *only* for the chosen peers — the server never
  obtains them (the F1/F6 gaps are about *authenticity*, not DM *confidentiality*).
- **Replay:** room/group/DM stores run `checkReplay` (From/DeviceID/Seq).
- **Save-As path safety:** the user-chosen *Save-As* sanitizes sender-supplied
  filenames (`filepath.Base` + reject `.`/`..`/separators, `saveattachment.go:336`)
  — F12 is the *internal cache* write, which does not.

---

## Full-audit coverage — complete

- [x] **Epoch-key lifecycle** → F7 (server-supplied recipients, `MemberHash`
      unverified) + F8 (no FS — old keys retained forever).
- [x] **AES-GCM nonce management** (all 10 `Encrypt` sites) → F9 (room reuses the
      epoch key with random nonces; every DM/group site uses a fresh per-message
      key — safe).
- [x] **Authenticity of deletes / reactions / un-reacts / pins** → F6 (none
      verified on receipt).
- [x] **Private key at-rest + keygen** → clean (above) + F10 (gated empty-pass
      branch, INFO).
- [x] **Attachment / file crypto (upload + download)** → group/DM files use
      fresh per-file keys (safe); room files reuse the epoch key; download
      integrity uses a *server-provided* hash → **F11** (room-file substitution);
      `fileID` unsanitized in the cache write → **F12** (path traversal). The
      user-facing Save-As path *is* sanitized (verified-correct).
- [x] **Randomness sweep** → clean (`crypto/rand` everywhere; no `math/rand`).
- [x] **Downgrade / skip-verify** → the *de facto* skip-verify **is** F1/F6 (no
      verification is performed on the normal receive paths at all); no separate
      negotiated-downgrade path was found.

## Recommended priority

1. **F12** — sanitize `fileID` before the cache write (`filepath.Base` + nanoid
   format check). Cheapest fix, removes an arbitrary-path-write / RCE-class
   primitive.
2. **F1 + F6** — wire up verify-or-drop for normal messages and
   delete/react/pin, reusing the edit path's pattern (domain tag, length-prefixed
   msgID, actor binding). Highest impact; closes the authenticity gap.
3. **F7 + F11** — verify `MemberHash` on epoch-key receipt (or document rooms as
   server-trusted for membership); commit the file content-hash in the E2E
   payload (closes room-file substitution).
4. **F2** — harden the `SignRoom`/`SignDM` canonical forms (do this *with* F1).
5. **F8 / F9 / F3 / F4 / F5 / F10** — document the FS posture; counter nonces;
   stronger safety number; stricter pin-on-change; key-length + empty-pass
   asserts.

> **Caveat restated:** this is a careful *code-level* review, not a professional
> audit or formal proof. It covers the **term client** only — the server
> (`sshkey-chat`) crypto, the wire-protocol spec, the SSH transport, and
> side-channels are out of scope. Commission a professional audit before
> adversarial use.
