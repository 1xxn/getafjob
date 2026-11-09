# Privacy Policy Template

> [!NOTE]
> This template is provided for operators self-hosting **getafjob**. It reflects the actual technical architecture,
> encryption controls, and data practices of the application. Replace bracketed placeholders (`[...]`) with your own
> operational details before publishing.

---

# Privacy Notice / Datenschutzerklärung

**Effective Date:** `[Date, e.g. 2026-01-01]`  
**Last Updated:** `[Date, e.g. 2026-01-01]`

## 1. Controller and Contact Information

The data controller responsible for the processing of personal data on this instance of **getafjob** is:

- **Operator / Controller:** `[Your Name or Organization Name]`
- **Address:** `[Postal Address]`
- **Email:** `[privacy@example.com or admin email]`
- **Hosting Location:** `[e.g., Self-hosted on dedicated server in Frankfurt, Germany / Hetzner Cloud]`

## 2. Overview and Principles

**getafjob** is a self-hosted platform that indexes job postings, calculates match relevance according to user-defined
rules, and assists in filling out job application forms in real browsers.

Our technical architecture is built around the following core data protection principles:
- **Zero Disguise:** We do not bypass bot protection or deceive hiring portals. Captchas and identity verifications are solved by the human user.
- **Never Invented Data:** Every form field filled by getafjob is backed by documented provenance rooted in facts provided directly by the user.
- **No Cover Letters:** getafjob strictly refuses to generate or submit automated cover letters.
- **Tenant Isolation:** Every user's data is isolated using PostgreSQL Row-Level Security (RLS) and per-user envelope encryption.
- **Cryptographic Shredding:** Deletion of an account destroys the encryption key, rendering any archived database records mathematically unrecoverable.

---

## 3. Data We Collect and Process

### A. Identity and Account Data
- **Contact:** Email address and display name.
- **Authentication:** Passkey credentials (FIDO2 / WebAuthn public keys, sign counters, transports). getafjob does not use or store passwords.
- **One-time tokens:** Cryptographic hashes (SHA-256) of invitation tokens, magic sign-in links, and session identifiers. Cleartext tokens are never stored in the database.
- **Active Sessions:** IP address hash, user agent, creation timestamp, and last activity timestamp.

### B. Profile and Applicant Data
- **Personal Details:** Full name, phone number, address, professional URLs (LinkedIn, GitHub, personal website).
- **Documents:** Resumes, CVs, and work certificates uploaded by the user.
- **Application Materials:** Structured education, work history, skills, language proficiencies, and user-defined text blocks.
- **Custom Question Answers:** Stored answers to common application questions (e.g. salary expectations, work authorization, notice periods).

### C. Matching and Application History
- **Search Queries and Preferences:** Job titles, keywords, location filters, remote preferences, and rule weights.
- **Matches:** Indexed job postings matched against user criteria, score breakdowns, and interaction state (saved, hidden, applied).
- **Applications:** Application statuses, submission timestamps, portal URLs, and submit register attempts (preventing duplicate submissions).
- **Execution Evidence:** Step-by-step audit records of form fields filled, provenance links to user profile facts, and verification screenshots.

### D. Security Audit Log
- Append-only security audit log recording authentication attempts, credential modifications, session revocations, and administrative actions. Audit records contain action codes, timestamps, and IP addresses, but no personal profile content.

---

## 4. Technical and Organizational Security Measures (TOMs)

getafjob employs defense-in-depth security controls (OWASP ASVS 4.0 Level 2 compliant):

1. **Envelope Encryption (AES-256-GCM):**
   Each user has a unique 256-bit AES data key. Profile data, text blocks, and uploaded files are encrypted before storage. The user data key is wrapped by a master key stored exclusively in container secrets (`GETAFJOB_MASTER_KEY_FILE`), never in the database.
2. **Database Row-Level Security (RLS):**
   PostgreSQL enforces tenant separation at the database engine level (`FORCE ROW LEVEL SECURITY`). Application queries execute under an unprivileged database role (`getafjob_app`) that cannot bypass RLS.
3. **Sealed Blob Storage:**
   Resumes and screenshots are stored encrypted on disk or S3-compatible storage under isolated tenant prefixes (`users/<user-id>/`).
4. **Sandboxed Browser Execution:**
   Automated form filling runs inside isolated Linux containers with Chromium user namespace sandboxing, restricted seccomp profiles, read-only root filesystems, dropped capabilities (`CAP_DROP: ALL`), and egress network proxies blocking internal networks and cloud metadata services.
5. **Transport Security:**
   All web traffic is encrypted via TLS 1.2+ with HSTS (`max-age=63072000; includeSubDomains`), strict Content Security Policy (CSP with per-response nonces and Trusted Types), and HTTP-only, SameSite=Lax session cookies (`__Host-` cookies).

---

## 5. Artificial Intelligence Policy

- **Opt-In Only:** AI capabilities (such as Claude API integration for parsing unstructured forms) are disabled by default (`GETAFJOB_AI_ENABLED=false`).
- **No Cover Letters:** The AI engine will refuse any instruction to generate cover letters.
- **No Automated Final Submission:** If AI assistance is enabled, draft responses must be explicitly reviewed and approved by the user before any application is submitted.
- **Provider:** When enabled by the operator, requests are sent via HTTPS to Anthropic (`api.anthropic.com`) using structured tool schemas. Job postings and minimal required profile facts are transmitted solely for field mapping.

---

## 6. Retention and Erasure Schedules

| Data Category | Retention Period | Deletion Mechanism |
|---|---|---|
| **Form Run Screenshots** | 90 days from capture | Automatic daily cleanup job / `admin cleanup` |
| **Expired Sessions & Links** | Immediately upon expiry (max 7–30 days) | Automatic scheduled worker |
| **Active User Profile & Documents** | Retained until user deletes account | On demand |
| **Deleted User Account** | Immediate permanent erasure | Crypto-shredding + blob purge + DB cascade |
| **Security Audit Logs** | `[e.g., 90–365 days]` | Operator retention schedule |

---

## 7. Your Rights Under GDPR (Articles 15–22)

Under the General Data Protection Regulation (GDPR) and applicable data privacy laws, you possess the following rights:

1. **Right of Access (Art. 15 GDPR):**
   You can review all profile data, matches, and application history directly within the web dashboard.
2. **Right to Rectification (Art. 16 GDPR):**
   You may modify or update any profile facts, answers, or documents at any time.
3. **Right to Erasure / "Right to be Forgotten" (Art. 17 GDPR):**
   You can delete your account at any time via **Account Settings > Privacy & your data > Delete account**.
   Deletion performs an irreversible **cryptographic shredding** operation:
   - The user's AES-256 data key is permanently zeroed and dropped (`crypto.Envelope.Forget`), rendering any encrypted backups unrecoverable.
   - All uploaded documents and screenshot files in the blob store are deleted.
   - All tenant rows across database tables are deleted via cascade.
4. **Right to Data Portability (Art. 20 GDPR):**
   You can download a complete copy of your data at any time via **Account Settings > Privacy & your data > Export all data**.
   The system generates a standard ZIP archive containing:
   - `profile.json` (personal details, education, work experience, skills)
   - `applications.json` (application history, submission attempts, filled values)
   - `matches.json` (job matches and ranking scores)
   - `settings.json` (search preferences and rule settings)
   - `audit_events.json` (security audit trail)
   - `documents/` (all original, decrypted resume and certificate files)
5. **Right to Restrict or Object to Processing (Arts. 18 & 21 GDPR):**
   You can pause crawling, disable matching, or disable automated form filling at any time in your account settings.

To exercise any of these rights outside the self-service web interface, contact the operator at `[Contact Email]`.

---

## 8. Operator Customization Checklist

When deploying this template for your instance:
1. Fill in the **Controller and Contact Information** in Section 1.
2. Confirm the **Hosting Location** and server jurisdiction.
3. If AI features are disabled, confirm Section 5 reflects `GETAFJOB_AI_ENABLED=false`.
4. If third-party storage (e.g. AWS S3, Cloudflare R2) is used instead of local disk storage, specify the subprocessor in Section 4.
5. If using transactional email (e.g. Resend, Postmark, AWS SES), list the email delivery provider under third-party processors.
