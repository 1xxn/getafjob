# Runbook: first installation on a bare server

From an empty Ubuntu server to a running getafjob with TLS, mail and backups. Everything later (updates, rollback,
staging) is in [deploy.md](deploy.md); backups in [backup-restore.md](backup-restore.md).

Placeholders: `example.org` is your domain, `203.0.113.10` / `2001:db8::10` the server's addresses, `<version>`
the image tag you run (a release such as `v0.1.0`, or `main-<sha>`). Commands that start with `$` run on your own
machine, `#` as root on the server, `deploy$` as the user `deploy` on the server.

## 1. What you need

| Item | Notes |
|---|---|
| Server | KVM virtual or dedicated server, 2 cores and 4 GB RAM for one person (one browser at a time), 4 cores and 8 GB for a few people or parallel runs; 40 GB disk or more. Ubuntu 24.04 LTS. OpenVZ or LXC containers do not work: the Chromium sandbox needs user namespaces and seccomp. |
| Domain | Any domain or subdomain whose DNS you control. The web app runs on it; mail goes out from an address on it. |
| Mail sending | An SMTP relay that sends for your domain (section 3). Without mail nobody can sign in: sign-in links come by mail. |
| Password manager | For the master key, the backup password and the SMTP key. Without the master key a restored database cannot be read. |
| Backup target | Off the server: an S3 bucket, a storage box (SFTP) or similar (section 9). |

## 2. DNS

At your domain's DNS (registrar or DNS host) set:

| Name | Type | Value | Why |
|---|---|---|---|
| `example.org` | A | `203.0.113.10` | web app (IPv4) |
| `example.org` | AAAA | `2001:db8::10` | web app (IPv6) |
| `example.org` | CAA | `0 issue "letsencrypt.org"` | only Let's Encrypt may issue certificates (optional; skip if your DNS host has no CAA) |
| mail records | TXT / CNAME | from your mail provider | section 3 |

Use a subdomain (`jobs.example.org`) instead of the domain itself if the domain serves something else; the mail
records stay on the domain. Wait until `dig +short example.org` returns the server's address before section 8:
Caddy needs it for the certificate.

## 3. Mail

Do not run a mail server on the getafjob host: providers often block port 25, a new IP has no sending reputation,
and mail from it lands in spam. Use a transactional mail service with SMTP; getafjob sends only sign-in links,
invitations and notifications, a few mails a day. A free plan is enough, for example Brevo (EU, 300 mails a day
free); any provider with SMTP and domain authentication works.

1. Create an account with the provider and add your domain as a sender domain.
2. Add the DNS records it shows: SPF (TXT on the domain, `v=spf1 include:… ~all`; if there is already an SPF record,
   add the `include:` to it, never publish two), DKIM (TXT or CNAME) and, if not there yet, DMARC
   (`_dmarc.example.org` TXT `v=DMARC1; p=none; rua=mailto:<an address you read>`; raise `p=quarantine` once mail
   goes out fine). Wait until the provider shows the domain as verified.
3. Create an SMTP key (not your account password) and note host, port and login.
4. Pick the sender address, e.g. `noreply@example.org`. It needs no mailbox when the domain is verified; if the
   provider insists on confirming the address, use a forwarding at your DNS host or create the key with an address
   you can read.
5. The SMTP URL for getafjob: `smtp://<login>:<key>@<host>:587` (STARTTLS) or `smtps://<login>:<key>@<host>:465`.
   Percent-encode `@`, `:` and `/` in login or key.

## 4. First login and hardening

```bash
$ ssh-keygen -t ed25519 -f ~/.ssh/getafjob -C getafjob-server
```

Give the public key (`~/.ssh/getafjob.pub`) to the provider when you order, or add it to `/root/.ssh/authorized_keys`
from the provider's console. Then:

```bash
$ ssh -i ~/.ssh/getafjob root@203.0.113.10
# apt update && apt -y full-upgrade
# timedatectl set-timezone Europe/Berlin
# adduser --disabled-password --gecos "" deploy
# install -d -m 0700 -o deploy -g deploy /home/deploy/.ssh
# install -m 0600 -o deploy -g deploy /root/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys
```

SSH with keys only and without root:

```bash
# cat > /etc/ssh/sshd_config.d/10-getafjob.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
EOF
# sshd -t && systemctl reload ssh
```

Before closing the root session, check in a second terminal that `ssh -i ~/.ssh/getafjob deploy@203.0.113.10`
works. Then the firewall and automatic security updates:

```bash
# ufw default deny incoming && ufw default allow outgoing
# ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp
# ufw enable
# apt -y install unattended-upgrades && dpkg-reconfigure -f noninteractive unattended-upgrades
```

Docker publishes ports past ufw; the Compose files publish only Caddy's 80 and 443, so nothing else is reachable.

## 5. Docker and cosign

Docker Engine and the Compose plugin from Docker's own repository (the Ubuntu package is older):

```bash
# install -m 0755 -d /etc/apt/keyrings
# curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
# echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
# apt update && apt -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
# usermod -aG docker deploy
```

Membership in `docker` is root on this host: keep the `deploy` key as safe as the root key.

cosign checks the image signatures before every deploy (required in production): download a pinned release and its
checksum file from github.com/sigstore/cosign/releases, compare with `sha256sum`, install it as
`/usr/local/bin/cosign`.

## 6. Code and secrets

```bash
# install -d -o deploy -g deploy /opt/getafjob /var/lib/getafjob/deploy
deploy$ git clone https://github.com/1xxn/getafjob.git /opt/getafjob
deploy$ cd /opt/getafjob && git checkout <version>
```

Secrets live in a root-owned directory, one file each, generated with the image itself:

```bash
# install -d -m 0700 /etc/getafjob/secrets
# cd /etc/getafjob/secrets
# for f in db_password db_app_password master_key runner_secret backup_password; do
    docker run --rm ghcr.io/1xxn/getafjob:<version> admin gen-secret > "$f"; done
# printf '%s' 'smtp://<login>:<key>@<host>:587' > smtp_url
# chmod 0600 *
```

Copy `master_key`, `backup_password` and the SMTP key into your password manager now, before any real data exists.

## 7. Settings

Compose reads `/opt/getafjob/deploy/.env`:

```bash
deploy$ cat > /opt/getafjob/deploy/.env <<'EOF'
GETAFJOB_DOMAIN=example.org
GETAFJOB_ACME_EMAIL=<an address you read; the certificate authority writes there>
GETAFJOB_MAIL_FROM=noreply@example.org
GETAFJOB_TIMEZONE=Europe/Berlin
GETAFJOB_SECRETS_DIR=/etc/getafjob/secrets
EOF
deploy$ chmod 0600 /opt/getafjob/deploy/.env
```

Everything else has defaults ([configuration.md](../configuration.md)): automation starts at "suggest" (nothing is
queued before a user turns it on), crawls run every 6 hours, matching uses the embedding model in the image.

## 8. Deploy

```bash
deploy$ cd /opt/getafjob
deploy$ GETAFJOB_ENV=production deploy/deploy.sh <version>
```

The script verifies the signatures, runs the migrations, starts the stack and waits for `https://example.org/healthz`;
on a failure it rolls back. Then check:

```bash
deploy$ docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml ps
```

Every service must be `healthy`. The runner is healthy only if its browser self-test passed: Chromium started
**with** its sandbox and opened a page. On Ubuntu 24.04 AppArmor restricts unprivileged user namespaces; if the
runner stays unhealthy, `docker compose … logs runner` names the cause. Never switch the sandbox off
(`--no-sandbox` is refused by the runner anyway); decide the host setting with the maintainer.

## 9. First admin and backups

Invite yourself as admin; the invitation goes out by mail, which also tests the mail setup:

```bash
deploy$ docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec serve \
    /usr/local/bin/getafjob admin invite -email <your address> -role admin -send
```

Open the link, choose your name, then add a passkey (Account settings). If no mail arrives, check
`docker compose … logs serve` and the provider's sending log.

Backups with restic to a target off the server ([backup-restore.md](backup-restore.md)):

```bash
deploy$ export RESTIC_REPOSITORY=sftp:<user>@<host>:/getafjob   # or s3:…, b2:…
deploy$ export RESTIC_PASSWORD_FILE=/etc/getafjob/secrets/backup_password GETAFJOB_SECRETS_DIR=/etc/getafjob/secrets
deploy$ deploy/backup.sh
```

Run it daily from a systemd timer or cron, and rehearse one restore before real data goes in. Snapshots of the whole
server from the provider are a good second layer, not a replacement: they live with the same provider.

## 10. Checklist

- [ ] `https://example.org` shows the sign-in page with a valid certificate; `http://` redirects
- [ ] All services `healthy`, the runner included (sandbox on)
- [ ] Invitation and sign-in mails arrive and are not marked as spam (SPF, DKIM, DMARC pass in the headers)
- [ ] `ssh root@…` and password logins are refused; only 22, 80 and 443 are open
- [ ] Master key, backup password and SMTP key are in the password manager
- [ ] One backup made and one restore rehearsed
