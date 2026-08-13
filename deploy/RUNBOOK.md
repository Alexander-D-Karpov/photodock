# PhotoDock server runbook

Steps that must run **on the server** (`sanspie@akarpov.ru`) or in the DNS/CDN
control panels. The application-side work for every task below is already merged;
this file is the operator checklist for the parts that cannot be done from a dev
checkout. Run them in order.

## Task 0 — deploy the current binary (do first)

An earlier deploy failed with `cp: Text file busy`. Confirm the running binary
matches the freshly built one and redeploy if not. The `make deploy` target now
uses `install` (unlinks instead of overwriting), which avoids the busy error.

```sh
make deploy          # builds linux/amd64, scp, then: sudo install + restart
# or verify manually:
md5sum ~/photodock/main /opt/photodock/main
sudo install -m 755 ~/photodock/main /opt/photodock/main
sudo systemctl restart photodock
```

Note: the repo's `photodock.service` has `ExecStart=/opt/photodock/photodock`,
while task notes describe the unit running `/opt/photodock/main`. Confirm which
path the deployed unit actually uses (`systemctl cat photodock`) and keep the
binary name consistent with it.

## Task 2 — EXIF reprocess + PATH (app side merged)

The exiftool flags (`-all -Orientation#`, no `-n`) and the portrait
dimension-swap now ship in the binary. On the server:

1. Ensure exiftool is on the service PATH. `photodock.service` now sets
   `Environment=PATH=...:/usr/bin/vendor_perl`. Reinstall the unit and reload:
   ```sh
   sudo cp photodock.service /etc/systemd/system/photodock.service
   sudo systemctl daemon-reload && sudo systemctl restart photodock
   ```
   (The `/usr/local/bin/exiftool` symlink can stay as a belt-and-braces backup.)
2. Trigger a reprocess and wait for completion:
   ```sh
   curl -u "$ADMIN_USER:$ADMIN_PASS" -X POST https://photos.akarpov.ru/admin/reprocess
   journalctl -u photodock -f   # wait for "Metadata reprocessing complete"
   ```
3. Verify in Postgres:
   ```sql
   SELECT filename, width, height,
          exif_data->>'orientation' AS o,
          exif_data->>'focus_mode'  AS focus
   FROM photos WHERE filename IN ('IMG_3101.JPG','IMG_3100.JPG');
   ```
   Expect numeric orientation ("1"/"6"), `focus` = "Single" (not "4"), and
   portrait photos with swapped dims (e.g. 3456×4608).

## Task 3 — Telegram link previews via Cloudflare

Telegram's crawler (149.154.160.0/20, 91.108.4.0/22) cannot reach the RU-hosted
origin, so no `TelegramBot` hits ever appear. Front the host with Cloudflare:

1. Add the `akarpov.ru` zone to Cloudflare (free), switch nameservers at reg.ru.
2. Orange-cloud (proxy) the `photos` A record only; keep other subdomains
   DNS-only (grey cloud).
3. SSL/TLS mode **Full (strict)** — the Let's Encrypt cert on the origin stays.
4. Confirm certbot renewals still pass (HTTP-01 via
   `/.well-known/acme-challenge/`, proxied through CF is fine):
   `sudo certbot renew --dry-run`.
5. Optionally restore real client IPs in nginx (CF `set_real_ip_from` ranges +
   `real_ip_header CF-Connecting-IP`).

Verify: `sudo tail -f /var/log/nginx/access.log | grep -i telegram` while sending
`https://photos.akarpov.ru/p/япония-2026/img_3081` to @WebpageBot — crawler hits
must appear via CF edge IPs, and @WebpageBot must return a rendered preview.

## Task 4 — nginx log rotation (urgent)

```sh
sudo cp deploy/logrotate-nginx /etc/logrotate.d/nginx
sudo logrotate -f /etc/logrotate.d/nginx
sudo systemctl enable --now logrotate.timer
df -h /var/log
```

The bulk of the volume is `location /`. `access_log off` is already set on
`/thumb/`, `/static/`, `/placeholder/`, and `/favicon.ico` in `nginx.conf`. To
cut write pressure further, buffer the main access log in the **production**
nginx `location /`:

```nginx
access_log /var/log/nginx/access.log combined buffer=64k flush=5s;
```

(The repo `nginx.conf` is the docker-compose variant that logs to stdout, so the
buffered file directive belongs only in the server's real config.)

## Task 5 — leftovers

- **favicon**: app now serves `/favicon.ico` (embedded WebP) and every template
  links `/static/icon.webp`; nginx has a cached `location = /favicon.ico`. No
  server action needed beyond deploying the new binary.
- **DNS resolver broken**: `/etc/resolv.conf` has no nameservers (dhcpcd
  generated it empty), so `go build` toolchain downloads and other DNS lookups
  fail. Point it at a working resolver, e.g.:
  ```sh
  printf 'nameserver 1.1.1.1\nnameserver 9.9.9.9\n' | sudo tee /etc/resolv.conf
  # or fix dhcpcd to emit nameservers and make resolv.conf immutable if needed
  ```
- **Natural folder ordering / collation**: already landed in code
  (`getFoldersWithCounts` ORDER BY name-prefix + embedded number desc; JS sorts
  use `Intl.Collator({numeric:true})`). No action.
