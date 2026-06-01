# Cryptographic Correctness Audit — sshkey-term (client)

Code-level review of the term client's end-to-end cryptography and, crucially,
its **enforcement**. Date: 2026-05-30.

> **Status: complete; implementation status refreshed 2026-05-31.** 12 findings
> (F1–F12): **four HIGH** (F1, F6, F7, F12), one MEDIUM (F11), the rest
> LOW/INFO. Every finding is fixed/resolved/documented; **F6 is partially
> resolved** — its **reaction** and **un-react** legs are now authenticated
> (verify-or-drop), message **delete** is specced and pending
> (`docs/planning/open/f6-delete-authentication.md`), and **pin/unpin** are
> accepted-by-design (low-stakes, ephemeral, never admin-gated — see §F6). The
> cryptographic primitives are sound; the original gaps were missing receive-side
> verification, one path-traversal in the file-cache write, and several
> defense-in-depth/UX inconsistencies. See the Bottom line and Recommended
> priority sections. Code-level review, not a professional audit.

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
| F1 | **HIGH** | Normal messages signed on send but never verified on receive (impersonation in rooms/group-DMs) — *✅ fixed 2026-05-30 (verify-or-drop on room/group/DM receipt)* |
| F6 | **HIGH** | Deletes / reactions / un-reacts / pins are not authenticated on receipt (server can forge + misattribute) — *◑ partially resolved 2026-05-31: **reactions** + **un-react** authenticated (verify-or-drop, cross-repo); **delete** specced + pending (`f6-delete-authentication.md`); **pin/unpin** accepted-by-design (low-stakes, ephemeral, never admin-gated)* |
| F7 | **HIGH** (design-dependent) | Room epoch-key recipients are the server's trigger list; `MemberHash` never verified → a malicious server can add an eavesdropper to a room — *✅ fixed 2026-05-31 (signed member attestation + verify-or-fail-closed, cross-repo)* |
| F12 | **HIGH** | Path traversal via unsanitized `fileID` in the download cache write → arbitrary-path file write — *✅ fixed 2026-05-30 (write/delete sites); read/render cache paths closed 2026-05-31 → `config.ValidFileID` now gates every fileID→path site* |
| F11 | MEDIUM | File-download integrity uses a server-provided hash; room files are substitutable — *✅ resolved 2026-05-31 (option (i): E2E-committed `ContentHash` in the signed Attachment, verified on download)* |
| F2 | LOW–MED | Send-path signatures lack length-prefixing / domain separation — *✅ resolved 2026-05-31 (domain-tagged + length-prefixed canonical forms; framed `wrappedKeysCanonical` + `MemberHash`)* |
| F8 | INFO (design) | No forward secrecy / post-compromise recovery — *✅ documented 2026-05-31 (protect the private key and local device)* |
| F9 | LOW | Room content reuses the epoch key with random 96-bit nonces (~2³² birthday bound per epoch) — *✅ documented 2026-05-31 (random nonces correct for the shared key; rotation holds the bound ~7 orders clear; counter nonces rejected as worse)* |
| F3 | LOW | `SafetyNumber` is entropy-lossy and short (24 digits) — *✅ resolved 2026-05-31; expanded 2026-06-01 (uniform full-hash encoding — `bigint(hash) mod 10³²` — removes the `%100` bias + uses all 32 bytes; display is now 32 digits in 8×4 groups)* |
| F4 | LOW | TOFU pin overwritten to the changed key before the user decides — *✅ resolved 2026-05-31 (immutable account-key mismatch now warns + rejects; old pin/cache stay active)* |
| F10 | INFO | Unencrypted key-generation branch exists — *✅ resolved 2026-05-31 (intentional user choice; strength now advisory-only + explicit blank warning, replacing the incoherent allow-blank-but-block-weak gate)* |
| F5 | INFO | `Encrypt`/`Decrypt` don't assert a 256-bit key — *✅ resolved 2026-05-31 (`len(key) == 32` guard on both; rejects 16/24-byte downgrade)* |

## Bottom line

The cryptographic **primitives are implemented correctly** — sound AEAD / ECIES
/ Ed25519 / HKDF construction, `crypto/rand` everywhere (zero `math/rand` in the
tree), correct Ed25519↔X25519 conversions, passphrase-encrypted keys at `0600`,
whole-DB SQLCipher at rest, no hardcoded secrets. **The main gap was enforcement
on the receive side**, and it is now **mostly closed**: message **edits**,
**normal messages** (room/group/DM — F1, fixed), room member attestations (F7,
fixed), and file **content-hashes** (F11, fixed) are verified-or-dropped. F6 is
now **partially closed**: **reactions** and **un-reacts** are verified-or-dropped
too (2026-05-31). Still on the server's word: message **deletes** (specced +
pending — `docs/planning/open/f6-delete-authentication.md`); **pins/unpins** are
accepted unauthenticated **by design** (low-stakes, ephemeral, never
admin-gated — §F6). Against the project's own "untrusted server" threat model,
that currently means:

- **DM / group-DM content confidentiality vs the server: holds** — per-message
  keys are wrapped only for the chosen peers; the server never sees them.
- **Room content confidentiality vs hidden-recipient injection: now holds** for
  the implemented room model — room epoch keys carry a signed member attestation,
  so a server-added shadow reader is detected/fail-closed (F7, fixed).
- **Message authenticity vs the server: now holds** for normal room/group/DM
  messages (F1, fixed) and *edits*. **Action authenticity now holds for
  reactions and un-reacts** (F6, 2026-05-31); it **does NOT yet hold for
  deletes** (F6, specced + pending); pins/unpins are accepted unauthenticated by
  design.
- **Security posture / F8:** The app provides E2EE against the server and encrypts local history at rest, but it does not provide cryptographic forward secrecy or post-compromise recovery. Protecting the private key and local device remains critical.
- **File handling vs the server: now holds** — room-file content substitution
  (F11, fixed) and the unsanitized-`fileID` path traversal (F12, fixed) are both
  closed: the recipient verifies an E2E-committed content hash on download, and
  every `fileID → path` use is gated by `config.ValidFileID`.

The encouraging part: **every gap was a missing verification call, not a broken
primitive** — and the **edit path already demonstrated the correct verify-or-drop
pattern.** That pattern has now been extended to the **normal-message handlers
(F1, fixed)**, room epoch-key receipt (F7, fixed), and attachment content hashes
(F11, fixed). It now also covers the **reaction** and **un-react** handlers (F6,
2026-05-31); extending it to the **delete** handlers (F6 — specced) is the last
authenticity gap before the strongest "server cannot forge actions" E2E claim
holds (pins/unpins excepted by design).

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

> **Fixed (2026-05-30).** `storeRoomMessage` / `storeGroupMessage` /
> `storeDMMessage` (`internal/client/persist.go`) now **verify-or-drop** every
> inbound message **before** it is decrypted or stored, mirroring the edit path:
> resolve the sender's Ed25519 key via `pubKeyForUser` (live profile → pinned-key
> fallback; **nil ⇒ drop**), base64-decode the payload + signature, then check
> `crypto.VerifyRoom` (room — over `payload ‖ room ‖ epoch`) or `crypto.VerifyDM`
> (group/DM — over `payload ‖ conversation ‖ wrapped_keys`). Any failure —
> unknown/unresolvable sender, un-decodable fields, or a bad signature — logs a
> `Warn` and returns without storing. A malicious server can no longer attribute a
> decryptable ciphertext to a sender who didn't sign it. Tests:
> `message_verify_test.go` (15 cases: valid → stored; garbage / unsigned /
> unknown-sender / context-rebound → dropped, across room/group/DM). **Scope:**
> uses the existing canonical forms — see **F2**, which was subsequently resolved.
> Does **not** cover deletes/reactions/un-reacts/pins (**F6**) — reactions +
> un-react have since been closed (verify-or-drop); delete is pending, pin/unpin accepted.
> The room `MemberHash` gap (**F7**) was subsequently closed with signed member
> attestation.

## F2 — Send-path canonical signing forms lack length-prefixing / domain separation (LOW–MED — ✅ resolved 2026-05-31)

`SignRoom = payload ‖ room ‖ epoch` and
`SignDM = payload ‖ conversation ‖ wrappedKeysCanonical`
(`internal/crypto/crypto.go:199,221`) concatenate adjacent **variable-length**
fields with no delimiters, no length prefixes, and no domain tag. Consequences:
- **Boundary ambiguity:** different field splits can yield identical signed
  bytes (`"AB"‖"CDE"` vs `"ABC"‖"DE"`), enabling signature *reinterpretation*
  given a valid signature.
- **Cross-context confusion:** a `SignRoom` signature and a `SignDM` signature
  can share a byte-string and cross-verify, because nothing domain-separates
  them.

The team already fixed exactly this for **edits**: `SignRoomEdit` /
`SignDMEdit` (crypto.go:255,279) prepend a domain tag (`"edit_room:"` /
`"edit_dm:"`) and a **length-prefixed** `msgID` — with a comment describing the
substitution attack. The base send-path forms never got the same treatment.
This was academic while F1 stood (nothing verified `SignRoom`/`SignDM`); **with
F1 now fixed — verification is live — hardening these forms moves from academic to
recommended.** In practice the AEAD layer still blocks the boundary/cross-context
reinterpretation (a reinterpreted signature only verifies if the substituted
ciphertext also decrypts under a key the server cannot forge a GCM tag for), so
this stays LOW–MED, but the forms should be hardened as defense-in-depth rather
than relying on that coincidence. (`MemberHash` concatenates usernames with no
delimiter too — same pattern; impact bounded by the nanoid username format.)

**Recommendation.** Adopt domain tags + length-prefixed variable fields for
`SignRoom`/`SignDM` (and `wrappedKeysCanonical` entries), as the edit forms do.

> **Scope verified against the code (2026-05-31).** All 10 `ed25519.Sign`/`Verify`
> sites are in `crypto.go`, so the three forms above are the **complete** set of
> unframed *signing* forms (`SignRoom` :199, `SignDM` :221, `wrappedKeysCanonical`
> :325). The edit forms and `buildEpochRosterCanonical` are already domain-tagged +
> length-prefixed (out of scope). Three implementation notes:
> - **`wrappedKeysCanonical` is shared by `SignDM` *and* `SignDMEdit`** (via
>   `buildDMEditCanonical`), whose own framing covers only the `msgID` — its
>   trailing keys blob is still unframed. So hardening `wrappedKeysCanonical`
>   (length-prefix + bind the username per entry) fixes both paths in one change.
> - **Client-only — no cross-repo lockstep.** The server implements none of these
>   forms; it relays/stores `member_hash`/`member_sig` opaquely. Each sign/verify
>   pair shares its canonical code (inline or a `build*` helper), so the
>   wire-signed bytes can change atomically (no users yet → no backwards-compat).
> - **`MemberHash` (:371) is the same pattern** — `SHA256(concat(sorted usernames))`
>   with no delimiter, signed indirectly via `SignEpochRoster`. Client-only +
>   symmetric (`epoch.go:58` sign / `epoch_verify.go:65` verify-compare).
>   **Bounded-safe in production**: usernames are a fixed 25 bytes (`usr_` + 21-char
>   nanoid, enforced by `GenerateID`/`ValidateNanoID`), so the concatenation is
>   unambiguous — but that is an *unenforced invariant at the call site* (test
>   fixtures already use variable-length names like `usr_alice_test`). Decide during
>   the fix: frame it (length-prefix/delimit) or document the invariant. See
>   discussion.

> **Resolved (2026-05-31).** Every signed form is now domain-tagged +
> length-prefixed via a shared `appendField` primitive — none of them depends on
> any ID/username length anymore:
> - `SignRoom`/`SignDM` got new domain tags (`"room:v1"` / `"dm:v1"`) and
>   length-prefixed `payload` / `room` / `conversation`.
> - `wrappedKeysCanonical` now length-prefixes **and binds the username** for each
>   entry (so `{alice:K}` and `{bob:K}` no longer sign alike) — which also frames
>   `SignDMEdit`'s trailing keys.
> - The edit forms (`buildRoomEditCanonical` / `buildDMEditCanonical`) were brought
>   to the same uniform framing (their `payload`/`room`/`conversation` had been raw
>   — only `msgID` was length-prefixed), closing the same residual.
> - `MemberHash` length-prefixes each username, removing the fixed-length-nanoid
>   reliance (it now feeds F7's `SignEpochRoster` unambiguously).
>
> Client-only — the server relays/stores these opaquely, sign+verify share each
> canonical builder (no drift), and there are no users (no migration). Tests
> (`crypto_test.go`): `TestSendCanonicalForm_DomainAndBoundary` (domain separation
> + payload/room boundary + username binding), the existing edit-form
> ambiguity/cross-context tests, and `TestMemberHash` (concatenation-collision
> case). Full term suite green.

## F3 — `SafetyNumber` is entropy-lossy and short (LOW — ✅ resolved 2026-05-31)

`SafetyNumber` (crypto.go:334) emits **24 decimal digits** (~80 bits) derived
from only **24 of the 32** SHA-256 bytes, via biased `byte % 100` reductions and
a `sum % 100` collapse (`int(hash[i])%100 + int(hash[i+12])%100`, then `% 100`,
for i = 0..11). It is not broken (≈2⁸⁰ work for a colliding MITM key), but it
discards entropy for no reason and is short next to comparable designs (Signal
uses 60 digits). **Recommendation:** encode the full 32-byte hash uniformly
(e.g. modular-reduce a big-integer view, or bytes→digits without `%100` bias)
and/or use more digits.

> **Resolved (2026-05-31; display expanded 2026-06-01).** `SafetyNumber`
> (`crypto.go`) now derives **32 digits** as `bigint(hash) mod 10³²` over **all 32**
> SHA-256 bytes — a uniform reduction (reduction bias ~2⁻¹⁵⁰, i.e. negligible)
> replacing the biased per-byte `%100` form that used only 24 of the 32 bytes. The
> verification UI renders the value as **8 groups of 4 digits** in **2 rows × 4
> columns**, keeping the compare surface compact while raising the displayed
> strength from ~80 bits to ~106 bits. Client-only; computed on the fly for
> display, so no persistence or migration. Tests: `crypto_test.go` —
> `TestSafetyNumber` (symmetry + length) and `TestSafetyNumber_DeterministicGolden`
> (fixed-seed golden value + 8×4-digit format, which would flip if the encoding
> regressed).

## F4 — TOFU pin overwritten to the changed key before the user decides (LOW — ✅ resolved 2026-05-31)

Before the fix, on a detected key change, `StoreProfile` (persist.go:755) warned
(`OnKeyWarning` at :775, which is **async** — pushes to a channel and returns)
and `ClearVerified` (:767), then **unconditionally** called `PinKey(new)`
(persist.go:779). The trust anchor therefore moved to the new (possibly attacker)
key *immediately*, ahead of the old modal decision — and because the
receive-side verify paths resolve the key via `pubKeyForUser`, the attacker's
*messages and edits* would then verify. A stricter immutable-key policy keeps the
**old** pin and never accepts the changed key in place.

The exposure actually opens **earlier than the pin**: the `profile` handler
overwrites the in-memory profile cache (`c.profiles[user] = &p`, `client.go:536`)
*before* it even calls `StoreProfile` (`:543`), and `pubKeyForUser` consults that
live cache **ahead of** the pinned-key fallback — so a forged-key `profile` frame
makes forged messages verify the instant it is processed, independent of (and
prior to) the durable pin overwrite. The pin overwrite is the offline/persistent
half of the same exposure.

Mitigated by the loud blocking modal + `ClearVerified` + the "no legitimate
rotation" invariant (any change is anomalous), but the pin-before-decision (and
cache-before-decision) weakens the warning's value. **Recommendation:** because
account keys are immutable in this app, do not accept a changed key in place at
all. Flag it immediately and keep the old trust anchor.

> **Resolved (2026-05-31).** Account keys are immutable, so a fingerprint mismatch
> is now treated as an account-identity violation rather than a key-rotation flow.
> The `profile` handler calls `StoreProfile` **before** writing the live profile
> cache. On mismatch, `StoreProfile` logs `ACCOUNT KEY CHANGED`, clears the local
> verified badge, fires the warning callback, returns `false`, and does **not**
> call `PinKey`; the profile handler then rejects the profile without replacing
> the cached public key/display name. Result: the old pin and old live verification
> key stay active, and forged messages signed by the changed key continue to fail
> receive-side verification. The TUI warning copy now states:
> `Account key changed for <name>. Account keys are immutable; this may indicate
> server/state corruption or compromise.` There is no "accept new key" action; the
> legitimate new-key path remains retiring the old account and approving a new
> account. Tests: `keywarning_dispatch_test.go` guards old-pin/live-cache
> preservation, and `keywarning_copy_test.go` guards the immutable-key modal copy
> plus the absence of an accept path.

## F5 — `Encrypt`/`Decrypt` don't assert a 256-bit key (INFO — ✅ resolved 2026-05-31)

`aes.NewCipher` accepts 16/24/32-byte keys, so a 16-byte key would silently
become AES-128. All keys are 32 bytes today (`GenerateKey`, HKDF output), but an
explicit `len(key) == 32` guard would prevent a future downgrade-by-bug.

> **Resolved (2026-05-31).** `Encrypt` and `Decrypt` (`crypto.go`) now reject any
> key whose length isn't exactly `keySize` (32) before reaching `aes.NewCipher`,
> returning a clear error instead of silently constructing an AES-128/192 cipher.
> The shared `keySize` const also backs `GenerateKey`, tying the "produce" and
> "require" sides to one number. No production caller is affected — every key
> comes from `GenerateKey` / epoch keys / HKDF output, all 32 bytes. Test:
> `crypto_test.go::TestEncryptDecrypt_RejectsNon256BitKey` (0/15/16/24/31/33/64-byte
> keys rejected by both functions; 32 accepted).

## F6 — Deletes / reactions / un-reacts / pins are not authenticated on receipt (HIGH)

> **Status (2026-05-31): ◑ partially resolved.** **Reactions** (part 1) and
> **un-react** (part 2) are now authenticated — verify-or-drop against the
> claimed actor's pinned key on every receive path (see the CHANGELOG and the
> per-row table notes below). **Delete** is specced and pending — full plan in
> `docs/planning/open/f6-delete-authentication.md`. **Pin/unpin** are
> **accepted-by-design** and stay unauthenticated: pins are never admin-gated, a
> forged pin/unpin only highlights/un-highlights a real already-authenticated
> message in ephemeral TUI-only state with no authority signal, and `unpinned`
> carries no actor at all (lowest value, highest cost). The finding text and
> table below are the original analysis, annotated with resolution status.

Generalizes F1 to message-mutation and metadata actions. None *was* verified on
receipt, and the **actor is read from a server-authored envelope field**:

| Action | Signed on send? | Verified on receive? | Actor source |
|--------|-----------------|----------------------|--------------|
| Delete / tombstone | **No** — `protocol.Delete` has no sig field | **No** — *specced + pending (`f6-delete-authentication.md`)* | `Deleted.DeletedBy` (server) |
| Reaction **add** | Yes — `SignRoom`/`SignDM` over the emoji ciphertext, the **same canonical form F1 verifies** | ✅ **Yes (2026-05-31)** — `VerifyReactionAuthor` on both `storeReaction` + `AddReactionDecrypted` | `Reaction.User` (server) |
| Reaction **remove** | ✅ **Yes (2026-05-31)** — new `SignUnreact` over `reaction_id` | ✅ **Yes (2026-05-31)** — `VerifyUnreactAuthor` (durable + TUI; cross-repo) | `ReactionRemoved.User` (server) |
| **Pin / Unpin** | **No** — *accepted by design* | **No** — *accepted by design (low-stakes, ephemeral, never admin-gated)* | `Pinned.PinnedBy` (server); `Unpinned` has none |

A malicious relaying server can therefore **forge a delete, reaction, or pin and
attribute it to any user**. (It can also **suppress** — drop a real delete,
message, or `reaction_removed` — but that is the relay's inherent power; signing
closes *forgery*, not censorship, so the fix's claim must be scoped to forgery,
exactly as for F1.) The reaction *send* path is the only one that already signs,
and — contrary to a first reading — that signature **is** reusable for
actor-authentication: it uses the exact `SignRoom`/`SignDM` canonical form F1
verifies, and F1 binds the author **not** by putting `From` in the signed bytes
but by **verifying against `pubKeyForUser(From)`** (`persist.go:80`). The same
move authenticates `Reaction.User`. The post-decrypt `dr.Target != r.ID` check
(`messages.go:1435`) additionally binds the target — but it lives **only in the
TUI merge path**; the durable `storeReaction` (`persist.go:620`) has no target
check, so even that partial guard is inconsistent.

**Recommendation — by action (status annotated 2026-05-31):**

1. **Reactions — ✅ done, client-only, no wire change.** Verify the *existing*
   signature against `pubKeyForUser(r.User)` on both receive paths
   (`VerifyReactionAuthor`, reusing `VerifyRoom`/`VerifyDM`); the `dr.Target != r.ID`
   target check was extended to the durable `storeReaction`; the orphan check was
   reordered ahead of the verify (security unchanged). No protocol/server change.
2. **Un-react — ✅ done, cross-repo.** New domain-separated `crypto.SignUnreact`
   binds the `reaction_id`; `SendUnreact` signs; `VerifyUnreactAuthor`
   verify-or-drops on the durable + TUI arms; the server relays the signature
   opaquely (live-only — removals aren't replayed, so no schema).
3. **Delete — ⏳ specced, pending.** Same shape (bind the msgID), but deletes are
   **replayed on catch-up**, so the server must *persist* the signature (a new
   column) and re-emit it on replay, and the client gates four receive paths. Full
   plan: `docs/planning/open/f6-delete-authentication.md`.
4. **Pin / unpin — 📝 accepted, unauthenticated by design.** Lowest value
   (only highlights/un-highlights a real, already-authenticated message; ephemeral
   TUI-only state; never admin-gated; `unpinned` has no actor) and highest cost
   (the bulk `Pins` catch-up would need per-pin provenance). Left as-is.

Both tiers follow the established **verify-or-drop** pattern (F1, edits, F7).
The actor never needs to live *inside* the signed bytes — key-selection against
the pinned key binds it, as F1 already demonstrates.

> **Server-side note (server audit §S1).** As with F1, the relay stamps
> `DeletedBy` / `PinnedBy` / reaction-`User` from the authenticated session and
> re-authorizes edits/deletes/unreacts against the *stored* sender — so a
> *co-member* cannot forge one of these attributed to another user. The forgery
> requires a **malicious server**, which is exactly the "Actor source (server)"
> column above. F6 stands as HIGH on that basis.

> **Audit re-verified 2026-05-31 (no code changes).** A three-way re-check
> (client receive, server relay, wire format) across both repos confirms F6 as
> written: all five actions are applied with no receive-side verification;
> reactions are signed-but-never-verified; delete/un-react/pin/unpin carry no
> signature field at all; `unpinned` carries no actor field either. The five are
> the **complete** set of unauthenticated *content/authorship* actions — every
> other inbound mutation (topic / rename / member add-remove / profile /
> read-receipt / typing / retire / device / epoch) is server-authoritative
> metadata that is plaintext-by-design per `encryption_boundaries`, and **edits**
> are the proof-by-contrast (the one mutation already verified). Two adjacent
> items stay **out of F6 by design** — group **promote/demote** (admin state
> applied on the server's word — the Phase-14 flat-admin model) and the
> server-authored **audit events** (`encryption_boundaries` decision) — flag them
> when scoping the fix so they aren't mistaken for missed gaps. (Some `file:line`
> refs in this section have drifted since the F11 edits; re-locate by symbol at
> implementation time — the findings are unaffected.)

## F7 — Room epoch-key recipients are server-supplied; `MemberHash` never verified (HIGH, design-dependent)

> **Fixed (2026-05-31) — signed member attestation + verify-or-fail-closed (cross-repo).** The key finding refined the fix: the missing primitive was a **generator signature**, not pubkey-binding — an *unsigned* `member_hash` is forgeable by the relay (it could rewrite it per-victim). Now the rotating client signs `(room, epoch, member_hash)` with its identity key (`crypto.SignEpochRoster`, domain `epoch_roster:v1`); the server stores it per `(room,epoch)` atomically with the key batch and forwards `generator/member_hash/member_sig` on every current-epoch `epoch_key`; each member verifies the signature against the generator's **pinned** key (`VerifyEpochRoster`), recomputes `MemberHash(local_roster)`, and **fail-closes** (does not adopt the key; warns) on any mismatch — with one `room_members` refresh first to absorb a lagging roster. A **sync-path guard** keeps sync/history keys decryption-only so they can't establish the current epoch and bypass the check. Username-set hash for v1 (key-substitution stays reactively self-detecting; device-pubkey binding deferred to the per-device-keys work via the `v1` version tag). Tests: server `epoch_attestation_test.go`; client `crypto/epoch_roster_test.go` + `client/epoch_verify_test.go` (valid→adopt; forged/missing/unknown-generator→fail-closed; mismatch→refresh-then-fail-closed; absent-roster→adopt-on-refresh). The server-side note below (honest server builds the recipient list authoritatively) still holds — F7 adds the cross-client check that makes a *malicious* server's covert injection detectable. Full design: `docs/planning/open/f7-room-member-attestation.md`.

Before the fix, `handleEpochTrigger` (`epoch.go:23`) wrapped the new room epoch
key for **`trigger.Members`** — the recipient list (usernames + pubkeys) supplied
by the server's `epoch_trigger` — with **no cross-check** against the rotating
client's own view of room membership:

```go
for _, member := range trigger.Members {
    pubKey, _ := crypto.ParseSSHPubKey(member.PubKey)
    wrapped, _ := crypto.WrapKey(epochKey, pubKey)   // wraps for whoever the server listed
    wrappedKeys[member.User] = wrapped
}
```

Before the fix, the client computed `crypto.MemberHash` over that same
server-supplied set and **sent** it (`epoch.go:54,62`), but **no receive path
verified it** — repo-wide, `MemberHash` had send-side references only;
`EpochConfirmed` had no hash field; receivers `UnwrapKey` and cached without
checking membership.

Consequence before the fix: a malicious server could **list itself or a colluder
in `epoch_trigger`**, causing a legitimate member to wrap the room key for the
attacker — who could then **decrypt every room message in that epoch**. Room
content was therefore not E2E-confidential against hidden-recipient injection by
an active malicious server.

**Design caveat:** rooms are operator-managed (rooms / groups / DMs use
deliberately different control models), so "the server/operator defines room
membership" may be an *intended* trust boundary. The fix keeps that product model
while adding a cross-client attestation: the rotator signs the member set and
recipients verify/fail-closed, making covert server-side recipient injection
detectable. DMs/group-DMs don't share the original gap — the sender wraps
per-message keys for peers *it* selects.

> **Server-side note (server audit §S1 / membership review).** The *honest*
> server builds the `epoch_trigger` recipient list from its own `room_members`
> table joined to each member's **bound** `users.key` (`epoch.go:300-309`), never
> from a client-supplied value, and no network user can self-join a room. So an
> honest server lists exactly the real members — the eavesdropper injection
> requires a **malicious server**, confirming the "design-dependent" framing: the
> client's missing `MemberHash` check is what makes that injection *undetectable*.

## F8 — No forward secrecy / post-compromise recovery (INFO — ✅ documented 2026-05-31)

The long-lived Ed25519 identity key unwraps **all** per-message keys (DMs/group
DMs) and decrypts **all** persisted epoch keys (rooms — old epoch keys are kept
indefinitely, in memory and in the SQLite `epoch_keys` table, to decrypt
history; no eviction on rotation, no key zeroization). No ratchet exists.
**Compromise of the identity private key ⇒ decryption of all past and future
messages** the user can see. A deliberate trade (history must survive
restart/sync), but state it plainly: the model gives confidentiality vs the
server + at-rest, **not** forward secrecy or post-compromise recovery.

> **Documented posture (2026-05-31).** The app provides E2EE against the server
> and encrypts local history at rest, but it does not provide cryptographic
> forward secrecy or post-compromise recovery. Protecting the private key and
> local device remains critical.

## F9 — Room content reuses the epoch key with random 96-bit nonces (LOW — ✅ documented 2026-05-31)

`crypto.Encrypt` uses a random 96-bit GCM nonce — safe only up to the birthday
bound (~2³² encryptions per key). **Room** messages, reactions, edits, and file
uploads all encrypt under the **same long-lived epoch key** (`send.go:90,487`,
`edit.go:83`, `filetransfer.go:111` via `UploadFile`), so all room activity in an
epoch accumulates random nonces under one key. A collision (≈50% near 2³²) would
leak a plaintext-XOR and the GCM auth key. Practically unreachable — epochs
rotate on every membership change **and on a server-enforced cadence (every 100
messages or 1 hour — `sshkey-chat/internal/server/epoch.go:36-37`,
`maxMessages`/`maxDuration`; the client only reacts to the server's
`epoch_trigger`, so the threshold lives server-side, not here)**, keeping per-key
nonce counts many orders of magnitude below 2³² — but a counter-based nonce (or a
documented per-epoch cap) would remove the bound. **DMs/group-DMs are immune**
(fresh per-message key → one nonce per key).

> **Documented posture (2026-05-31) — accepted, no code change.** Random 96-bit
> nonces are the **correct, stateless** choice for the room epoch key, which is
> *shared across all members and written by many senders concurrently with no
> coordination*. The birthday bound is held into irrelevance by rotation: a single
> epoch key encrypts on the order of ~100 messages (plus their reactions / edits /
> file uploads) before the 100-msg / 1-hour cap rotates it, so the collision
> probability ≈ q²/2⁹⁷ ≈ **2⁻⁷⁷** at q ≈ 10³ — roughly **seven orders of magnitude
> below** NIST SP 800-38D's conservative 2³²-per-key limit for random nonces.
>
> A **counter** nonce is deliberately *rejected* as a net negative here: for a
> shared, multi-writer, crash-prone key, a counter reset (crash / restore-from-
> backup / bug) or two senders sharing a counter value is **guaranteed** nonce
> reuse — catastrophic for GCM (reveals the GHASH key → tag forgery + plaintext
> XOR) — i.e. a far likelier *and* worse failure than the 2⁻⁷⁷ random-collision
> risk it would remove. Random nonces are stateless and crash-safe; that property
> is what makes them right for this setting. (DMs/group-DMs sidestep the question
> entirely via fresh per-message keys.) The only real invariant is that epoch
> rotation stays well below the bound, which the server-enforced 100-msg / 1-hour
> cadence satisfies with enormous margin. Accepted as a sound design property; no
> client change.

## F10 — Unencrypted key-generation branch (INFO — ✅ resolved 2026-05-31)

`generateEd25519KeyFile` (`internal/tui/keygen.go:37-41`) writes an
**unencrypted** OpenSSH key when `passphrase == ""` (else
`MarshalPrivateKeyWithPassphrase`, bcrypt-pbkdf). This branch is **intentional
and supported** — a blank passphrase is the user's choice, matching `ssh-keygen`
(which allows empty passphrases and enforces no strength when one is set). The
real issue was never the branch; it was the *inconsistency* in how the UI
treated it.

> **Resolved (2026-05-31).** The original write-up here described the UI as
> gating this with "mandatory passphrase validation (12-char min + zxcvbn
> floor)" — which was both **inaccurate** and **incoherent**:
>
> - *Inaccurate:* a blank passphrase was always allowed. The validator's
>   `pass == ""` → "passphrase is required" branch was **dead code** — both
>   callers (`wizard.go`, `addserver.go`) short-circuited it with a `pass != ""`
>   guard, so empty never reached validation.
> - *Incoherent:* a blank passphrase sailed through, but a *non-blank* one under
>   12 chars (or below zxcvbn score 2) was **hard-blocked**. The policy forbade
>   the middle while permitting both ends — nudging friction-averse users toward
>   the strictly-worse unencrypted option, since "weak-but-something" is strictly
>   more at-rest protection than "nothing."
>
> Passphrase strength is now **advisory-only** for user keys
> (`internal/keygen/strength.go`): the `MinPassphraseLength` floor is removed,
> `ValidationResult.Blocked` is never set (kept vestigial for call/test shape;
> `TestValidationResult_NeverBlocks` enforces it), weak/short non-blank
> passphrases get a live warning but submit freely, and the **blank** choice now
> surfaces an immediate explicit warning (`UnencryptedKeyWarning` — "anyone with
> the key can access this account"). That warning matters more than any
> weak-passphrase gate: a blank passphrase also leaves **local message history
> readable**, since the SQLCipher DB key derives from the same Ed25519 seed.
> Both keygen flows drop the old block / warn-and-confirm gate; the
> confirm-passphrase match check is unchanged. The original "hard-fail on empty
> inside `generateEd25519KeyFile`" recommendation is **deliberately not taken** —
> the opposite direction was chosen: keep the branch, make it a coherent,
> informed user choice. The server-side **admin** keygen stays strict
> (hard-blocks at zxcvbn score 2 — different blast radius). Tests:
> `internal/keygen/strength_test.go` + `livehint_test.go`,
> `internal/tui/{addserver,wizard,phase16_strength}_test.go`.

## F11 — File-download integrity check uses a server-provided hash; room files are substitutable (MEDIUM — ✅ resolved 2026-05-31)

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

> **Scope verified + fix options (2026-05-31).** Re-checked against both repos —
> the finding holds, and nothing else is a missed blob-download surface:
> `ThumbnailID` is vestigial (thumbnails are generated *locally* from the
> downloaded original; the server has no thumbnail handling) and `AvatarID` is
> profile metadata the client never downloads/renders (profiles are
> server-authoritative public data anyway). Details that bear on the fix:
> - `Size`/`Mime` are carried in the E2E `Attachment` but **never verified** against
>   the blob (`DownloadFile` checks only the server hash + the GCM tag) — binding
>   material that already exists but is unused.
> - The sender **already computes** `crypto.ContentHash(encBytes)` at upload
>   (`filetransfer.go:118`); today it's relayed via the server (hence swappable),
>   not E2E-committed.
> - Room files are sealed with **no AEAD additional data** (`gcm.Seal(…, nil)` in
>   `crypto.Encrypt`), so the GCM tag binds only (epoch key, nonce, plaintext) — not
>   the `FileID`.
> - Impact: room images **auto-download + render** (`maybeAutoPreviewAttachments`),
>   so a substitution is zero-interaction; and the swap pool is bounded to
>   *same-room + same-`FileEpoch`* files (the blob must decrypt under the victim's
>   exact epoch key).
>
> **Two clean fixes to weigh:**
>
> **Option (i) — content hash in the E2E metadata.** The sender adds
> `ContentHash(ciphertext)` to the `Attachment` struct (a value it already
> computes); the recipient verifies the downloaded blob against *that* instead of
> the server-relayed `download_start` hash. *Pros:* localized — one new payload
> field + one verify call, reuses an existing hash, no crypto-API change. *Cons:* a
> second integrity mechanism alongside the GCM tag; the `download_start` hash stays
> as a cheap pre-decrypt corruption check.
>
> **Option (ii) — bind a per-file identifier into the file-AEAD AAD.** In the
> abstract, pass `FileID` as GCM additional-authenticated-data when sealing the
> file, so a substituted blob fails the existing GCM tag even under the same epoch
> key — the decrypt step alone detects the swap, no new field, no separate verify.
> *Feasibility blocker:* **the `FileID` is server-assigned *after* the client
> encrypts.** `uploadEncrypted` runs `crypto.Encrypt` → `ContentHash` → mints a
> client-side `up_` upload-ID, and the server returns the real `fileID` only after
> `upload_start` (via the `pending.fileID` channel). So the client can't bind the
> real `FileID` into the ciphertext at encrypt time. Making (ii) work needs either
> **client-generated `fileID`s** (a server/protocol change — the server currently
> mints + format-validates them, F12) *or* binding a **different value known at
> encrypt time** (e.g. a sender-chosen `fileSalt`) as AAD and carrying *that* in the
> `Attachment` — which reintroduces a new payload field anyway, on top of threading
> an AAD parameter through `crypto.Encrypt`/`Decrypt`. So (ii)'s "no new field /
> single mechanism" advantage does **not** survive the server-assigns-`fileID`
> reality.
>
> Both would have closed the room-file substitution, but **the
> server-assigned-`fileID` constraint tilted strongly toward (i)**: it was feasible
> with zero protocol change and reused the hash the client already computed,
> whereas (ii) needed a new per-file field *and* AAD plumbing *and* (for the
> pure-`FileID` form) a protocol change. Option (i) was chosen and implemented.

> **Resolved (2026-05-31) — option (i).** The sender now commits
> `crypto.ContentHash(ciphertext)` into a new `Attachment.ContentHash` field
> (`internal/protocol/messages.go`). Because the `Attachment` lives inside the
> encrypted, **signed** message payload, that hash is E2E-confidential *and*
> sender-authenticated for free by the existing `SignRoom`/`SignDM` verification
> (F1/F2) — no new signature. The recipient (`DownloadFile`) verifies the
> downloaded blob against this E2E copy, preferring it over the server-relayed
> `download_start` hash (which it keeps only as a corruption fallback). So a
> malicious server substituting another same-room/same-epoch blob (which decrypts
> cleanly under the shared epoch key and carries a matching *relayed* hash) is now
> detected. The hash is threaded through all three representations
> (`protocol.Attachment` → `store.StoredAttachment` → `tui.DisplayAttachment`) so
> both the manual open/save and the **auto-preview** download verify it. The
> sender already computed this hash for upload, so it's reused, not recomputed.
> Client-only (the server relays the field opaquely, inside the ciphertext);
> attachment rows are a JSON blob so the new field needs no schema migration.
> Tests: `filetransfer_test.go::TestDownloadFile_RejectsServerSubstitutedBlob` (a
> swapped blob whose *server* hash matches but whose *E2E* hash doesn't is
> rejected). Option (ii) (FileID-as-AAD) was set aside — the server assigns the
> fileID *after* the client encrypts, so it would need a protocol change for no
> net gain over (i).

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

> **Fixed (2026-05-30; read paths closed 2026-05-31).** The path-safety
> invariant lives in one place — `config.ValidFileID` (rejects any fileID that
> isn't a safe single path component: empty / `.` / `..` / path separators / NUL;
> requires `Base(fileID) == fileID`) — with client `validFileID` delegating to
> it so both the `client` and `tui` packages share the same check.
>
> **Write/delete sites (2026-05-30):** the **download** cache write
> (`DownloadFile`, plus `filepath.Base` at the write as defense-in-depth), the
> **upload** cache write (`AttachmentPath`, skipped on an unsafe id), and — beyond
> the original finding — `cleanupAttachmentFiles`' `os.Remove`, which was an
> **arbitrary-file-delete** via the same root cause.
>
> **Read/render sites (2026-05-31):** the remaining cache-hit paths are now gated
> too, so the same fileID can't be turned into a local path that gets stat'd,
> read, or decoded. These are *not* `os.Stat`-only as previously noted — they feed
> the inline-image renderer: `tui/messages.go`'s `attachmentLocalPath`
> (→ `SelectedImagePath` → `RenderImageInline`, which **opens + decodes** the
> file), `persist.go`'s auto-preview cache check (the size gate uses the *claimed*
> `a.Size`, not the actual file), and `app.go`'s save-attachment cache check +
> render-cache invalidation. Left unfixed, a traversal fileID from the (sender-
> controlled) E2E attachment metadata was a weak **file-existence oracle** plus a
> path to **read + decode the recipient's own local file** in their own view (no
> exfiltration to the attacker, no write/delete) — low severity, but a real
> path-confusion residual feeding an arbitrary local file to the image decoder.
>
> Legitimate fileIDs (`file_` + nanoid) are unaffected. Tests:
> `config/paths_test.go` (`TestValidFileID`, canonical),
> `client/filetransfer_fileid_test.go` (write paths, via delegation),
> `tui/attachment_path_guard_test.go` (read path rejects a traversal that would
> otherwise resolve to a real file outside `filesDir`).

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
  (See F10 for the user-chosen empty-pass branch — advisory-only, resolved.)
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

- [x] **Epoch-key lifecycle** → F7 (server-supplied recipients / `MemberHash`
      verification — fixed with signed member attestation) + F8 (no FS — old keys
      retained forever).
- [x] **AES-GCM nonce management** (all 10 `Encrypt` sites) → F9 (room reuses the
      epoch key with random nonces; every DM/group site uses a fresh per-message
      key — safe).
- [x] **Authenticity of deletes / reactions / un-reacts / pins** → F6 (none
      *was* verified on receipt; reactions + un-react now are, 2026-05-31; delete
      pending; pin/unpin accepted-by-design).
- [x] **Private key at-rest + keygen** → clean (above) + F10 (user-chosen
      empty-pass branch, INFO — resolved 2026-05-31, strength now advisory-only).
- [x] **Attachment / file crypto (upload + download)** → group/DM files use
      fresh per-file keys (safe); room files reuse the epoch key; download
      integrity previously used only a *server-provided* hash → **F11** (fixed
      with E2E-committed `Attachment.ContentHash`); `fileID` was unsanitized in
      the cache write → **F12** (fixed with `config.ValidFileID`). The user-facing
      Save-As path *is* sanitized (verified-correct).
- [x] **Randomness sweep** → clean (`crypto/rand` everywhere; no `math/rand`).
- [x] **Downgrade / skip-verify** → the *de facto* skip-verify was F1/F6 (the
      normal receive paths performed no verification). **F1 is now fixed** (normal
      messages verify-or-drop); F6 reactions + un-react now verify-or-drop too
      (2026-05-31), delete pending, pin/unpin accepted. No separate
      negotiated-downgrade path was found.

## Recommended priority

**Remaining — F6 (HIGH, partially resolved); delete is the last open leg:**

- **Reactions — ✅ done (2026-05-31).** Client-only verify-or-drop reusing the
  existing `SignRoom`/`SignDM` signature (`VerifyReactionAuthor`).
- **Un-react — ✅ done (2026-05-31).** Cross-repo: new `SignUnreact` over the
  `reaction_id`; `VerifyUnreactAuthor` on both receive arms; server relays opaquely.
- **Delete — ⏳ the remaining work.** Bind the msgID, but deletes are replayed on
  catch-up, so the server must **persist + re-emit** the signature and the client
  gates **four** receive paths. Full plan:
  `docs/planning/open/f6-delete-authentication.md`.
- **Pin / unpin — 📝 accepted-by-design** (low-stakes, ephemeral, never
  admin-gated). Scope the F6 claim to *forgery* (a malicious relay can still
  suppress); group promote/demote + server-authored audit events stay out by design.

**Completed:**

1. **F12** — ✅ **Done (2026-05-30; read paths closed 2026-05-31):** the
   path-safety check is now `config.ValidFileID` (client `validFileID` delegates),
   gating **every** server/sender-fileID → path site. Write/delete (2026-05-30):
   download + upload cache writes + the `cleanupAttachmentFiles` `os.Remove`
   (removed the arbitrary-path-write / RCE-class primitive and an arbitrary-delete).
   Read/render (2026-05-31): `attachmentLocalPath`, the auto-preview cache check,
   and `app.go`'s save-cache + render-cache-invalidation — closing the
   existence-oracle / read-and-decode-own-file residual. Tests:
   `config/paths_test.go`, `filetransfer_fileid_test.go`,
   `tui/attachment_path_guard_test.go`.
2. **F1** — ✅ **Done (2026-05-30):** verify-or-drop on every inbound room/group/DM
   message (`SignRoom`/`SignDM` against the sender's pinned key, mirroring the edit
   path) — `persist.go` `storeRoomMessage` / `storeGroupMessage` / `storeDMMessage`.
   Test: `message_verify_test.go`. **F6** applies the same verify-or-drop +
   actor-binding to the action handlers: **reactions** + **un-react** done
   (2026-05-31); **delete** is the remaining leg (cross-repo, with a
   persisted+replayed signature — see `f6-delete-authentication.md`); pin/unpin
   accepted-by-design.
3. **F7** — ✅ **Done (2026-05-31):** signed room member attestation + verify-or-fail-closed
   (cross-repo; rotator signs `(room,epoch,member_hash)`, members verify against the
   generator's pinned key and compare to their own roster, with a sync-path guard). See
   the F7 section. **F11** — ✅ **Done (2026-05-31):** the sender commits the file
   content-hash in the E2E (signed) Attachment and the recipient verifies the
   download against it, closing room-file substitution (see F11 section, option (i)).
4. **F2** — ✅ **Done (2026-05-31):** domain-tagged + length-prefixed canonical
   forms across `SignRoom`/`SignDM`/edits, with `wrappedKeysCanonical` (username-bound)
   and `MemberHash` framed too — no boundary/cross-context ambiguity remains, and
   none of it relies on ID/username lengths. Client-only; see above.
5. **F8** — ✅ **Done (2026-05-31):** documented the FS/PCS posture in the
   security audit and README: E2EE against the server + encrypted local history,
   but no cryptographic forward secrecy or post-compromise recovery.
6. **F3** — ✅ **Done (2026-05-31):** uniform full-hash safety number
   (`bigint(hash) mod 10³²`) — removes the `%100` bias + 8 discarded bytes; display
   expanded to 32 digits in 8×4 groups (2 rows × 4 columns), about 106 uniform bits. *(Also resolved/documented
   2026-05-31: F4 immutable account-key warn+reject; F5 256-bit key guard; F9
   random-nonce design accepted; F10 advisory-only passphrase. See each section.)*

> **Caveat restated:** this is a careful *code-level* review, not a professional
> audit or formal proof. It covers the **term client** only — the server
> (`sshkey-chat`) crypto, the wire-protocol spec, the SSH transport, and
> side-channels are out of scope. Commission a professional audit before
> adversarial use.
