# Manual Smoke Test (real TIDAL account)

> **MANUAL TEST. EXCLUDED FROM AUTOMATED GATES.**
> This checklist is run by a human operator against a REAL TIDAL HiFi Plus
> account. It is intentionally **not** part of `make tests`, CI, or the F1-F4
> review wave. Nothing here is asserted by an automated gate. It exists so a
> person can confirm, end to end, that tidal-syncer pulls true lossless audio
> from a live account with correct metadata.

## Before you start

- You need an active **TIDAL HiFi Plus** subscription (required for
  `HI_RES_LOSSLESS`).
- Have at least a few favorite tracks, one favorite album, and one favorite
  playlist on the account so the sync has something to fetch.
- Install the inspection tools used below: `ffprobe` (part of ffmpeg) and
  `metaflac` (part of the `flac` package). The container already bundles
  ffmpeg, but `ffprobe`/`metaflac` are run on the **host** against the synced
  files.
- Pick a working directory on the host with config, music, and data volumes,
  for example:

  ```sh
  mkdir -p ./smoke/{Music,data}
  cp config.example.yaml ./smoke/config.yaml
  ```

- **Never paste real tokens, refresh tokens, client secrets, or signed CDN
  URLs into this document, an issue, or a log you share.** Redact them.

---

## 1. Build and run

1. Build the binary (or image):

   ```sh
   make build          # host binary at ./build/package/tidal-syncer
   # or
   make docker-build   # container image
   ```

2. Confirm the build is wired up:

   ```sh
   ./build/package/tidal-syncer version
   ```

   Expect non-empty `Version`, `CommitHash`, and `BuildTime`.

3. Decide how you will run the rest of the checklist: directly with the host
   binary plus `--config ./smoke/config.yaml`, or via the container with the
   `./smoke` directory mounted to `/app`. Use one consistently.

---

## 2. Device login

1. Start the login flow:

   ```sh
   ./build/package/tidal-syncer login --config ./smoke/config.yaml
   ```

2. Read the **logs**. The command logs a verification link of the form
   `https://link.tidal.com/{code}`.

3. Open that link in a browser, sign in to the same TIDAL account, and
   authorize the device.

4. Wait for the command to report success.

5. Confirm the token was **persisted** to the state database:

   ```sh
   ls -la ./smoke/data            # state DB present (SQLite + WAL files)
   ```

   - [ ] Login link appeared in the logs.
   - [ ] Authorization succeeded.
   - [ ] A token row now exists in the state DB (do not print its contents
         into anything shared).

---

## 3. One-shot sync of real favorites

1. Run a single sync cycle:

   ```sh
   ./build/package/tidal-syncer sync --once --config ./smoke/config.yaml
   ```

2. Watch the per-cycle summary in the logs (counts of downloaded, skipped,
   failed).

3. Verify files landed at the **configured template path** under
   `paths.music`. With the default template
   `{albumartist}/{album}/{track} - {title}.{ext}` you should see:

   ```sh
   find ./smoke/Music -name '*.flac' | head
   # ./smoke/Music/<Album Artist>/<Album>/01 - <Title>.flac
   ```

   - [ ] Favorite tracks downloaded.
   - [ ] Files sit at the expected templated path.
   - [ ] Summary counts look sane (no unexpected mass failures).

4. **Idempotency:** run the same `sync --once` again. The second run should
   download nothing new and report the tracks as already done.

   - [ ] Re-run is a no-op (no re-downloads).

---

## 4. Confirm TRUE HI_RES_LOSSLESS on disk

Pick a sample of downloaded files and inspect their real bit depth and sample
rate. A HiFi Plus track should be lossless: at minimum 16-bit / 44.1 kHz, and
for hi-res masters up to 24-bit / 192 kHz.

1. With `ffprobe`:

   ```sh
   ffprobe -v error -show_entries stream=codec_name,sample_rate,bits_per_raw_sample \
     -of default=noprint_wrappers=1 "<path-to>.flac"
   ```

   Expect `codec_name=flac`, `sample_rate >= 44100`, and a bit depth of 16
   or more (commonly reported via `bits_per_raw_sample`).

2. Cross-check with `metaflac`:

   ```sh
   metaflac --list --block-type=STREAMINFO "<path-to>.flac" \
     | grep -E 'sample_rate|bits-per-sample'
   ```

   - [ ] Codec is FLAC.
   - [ ] Sample rate is at least 44100 Hz.
   - [ ] Bit depth is at least 16 (ideally 24 on hi-res masters).
   - [ ] At least one sampled track shows a true hi-res value
         (for example 24-bit / 96000 or 24-bit / 192000) when the album is a
         hi-res master.

---

## 5. Validation spike #2: manifest type and direct playback

This step records HOW the lossless stream is delivered. There is no automated
reference for `playbackinfopostpaywall`, so this is verified live.

1. For several sampled tracks, note which manifest type the lossless stream
   arrived as. Enable verbose logging if needed:

   ```sh
   ./build/package/tidal-syncer sync --once --verbose --config ./smoke/config.yaml
   ```

   For each sampled track, record the observed `manifestMimeType`:

   - `application/vnd.tidal.bts` -> direct FLAC GET (pure-Go path), or
   - `application/dash+xml` -> DASH/MP4 demuxed via the bundled ffmpeg path.

   | Track | manifestMimeType | Path taken (BTS / DASH) |
   |-------|------------------|-------------------------|
   |       |                  |                         |
   |       |                  |                         |
   |       |                  |                         |

2. **Confirm no external proxy is required.** The raw
   `playbackinfopostpaywall` call, made with the built-in Fire-TV
   credentials, must return a **usable HI_RES_LOSSLESS manifest directly**.
   The reference implementation (tidalgram) delegated this to an external
   proxy; verify that tidal-syncer does **not** need one.

   - [ ] At least one sampled track returned a usable HI_RES_LOSSLESS manifest
         directly from `playbackinfopostpaywall` (no proxy involved).
   - [ ] Manifest type per sampled track recorded in the table above.

3. **Encrypted tracks (expected behavior, not a bug):** if a track's manifest
   reports an `encryptionType` other than `NONE`, tidal-syncer is expected to
   **skip it and log the reason**. Confirm such tracks are skipped, not
   force-downloaded. No DRM/Widevine handling is in scope.

   - [ ] Any encrypted track was skipped with a logged reason.

---

## 6. Metadata: cover, lyrics, sidecar, album art

Inspect a downloaded track and its album folder.

1. Embedded cover art is present:

   ```sh
   ffprobe -v error -show_streams "<path-to>.flac" | grep -i 'video\|attached_pic'
   # or
   metaflac --list --block-type=PICTURE "<path-to>.flac"
   ```

2. Embedded lyrics are present in the tags:

   ```sh
   metaflac --show-tag=LYRICS "<path-to>.flac"
   ```

3. The synced `.lrc` sidecar sits next to the audio file:

   ```sh
   ls "<same-dir>/<same-basename>.lrc"
   ```

4. The album folder contains `folder.jpg`:

   ```sh
   ls "<album-dir>/folder.jpg"
   ```

   - [ ] Embedded front cover present (PICTURE type 3).
   - [ ] Embedded lyrics present in tags.
   - [ ] `.lrc` sidecar present and synced (timestamps look correct).
   - [ ] Album `folder.jpg` present.

---

## 7. Removal policies on a real un-favorite

Run each policy in a clean-ish setup so you can observe the effect of removing
a track from the remote library.

1. Note a track currently synced locally. **Un-favorite** it in the TIDAL app.

2. **keep** (default): set `removal.policy: keep`, run `sync --once`.

   - [ ] Local file is **left in place** (un-favoriting does not delete it).

3. **mirror**: set `removal.policy: mirror`, run `sync --once`.

   - [ ] Local file is **deleted** to match the remote library.

4. **trash**: set `removal.policy: trash`, run `sync --once`.

   - [ ] Local file is **moved to the trash folder**, not hard-deleted.

   Note: the first sync run never deletes anything. Make sure a prior snapshot
   exists before testing `mirror`/`trash`.

---

## 8. Daemon: poll pickup and graceful shutdown

1. Start the daemon (short interval speeds up the test):

   ```sh
   ./build/package/tidal-syncer daemon --config ./smoke/config.yaml
   # set daemon.interval to e.g. 1m in config first
   ```

2. While it runs, **add a new favorite** track in the TIDAL app.

3. Wait up to one interval and watch the logs.

   - [ ] The daemon's next poll picks up the newly-added favorite and
         downloads it within the configured interval.

4. **Graceful stop:** send SIGTERM and confirm it shuts down cleanly within
   the grace period (under ~5s; compose uses `stop_grace_period: 30s`).

   ```sh
   kill -TERM <daemon-pid>      # host
   # or
   docker compose stop          # container
   ```

   - [ ] Process logs a graceful shutdown and exits 0 within the grace period.
   - [ ] No `.part` temp files are left behind (a startup sweep would also
         clean these, but a clean stop should not orphan a completed file).

---

## Sign-off

- [ ] All sections above passed against a real HiFi Plus account.
- [ ] No real credentials, tokens, or signed URLs were recorded here or in any
      shared log.
- [ ] Manifest-type observations (Section 5) captured for the sampled tracks.

Date: ____________  Operator: ____________  Build version: ____________
