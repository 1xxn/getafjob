# User guide

A short tour of getafjob for the people who use it every day. Screens are in English and German; switch the
language in the account menu at the top right.

## Sign in

An admin invites you with a link. Open it, enter your name, and you are in. Later you sign in with a link sent to
your mail address, or with a passkey: add one under Settings, Account and security. With a passkey, a mail link
alone no longer opens your account.

## Set up (about five minutes)

The setup at `/setup` has five steps. You can skip any of them and come back later; `/setup` continues where
something is still missing.

1. **CV**: upload a PDF or Word file. getafjob reads it on the server, without AI, and fills empty fields.
2. **Details**: check what the import found. Applications use exactly these values.
3. **Search**: job titles and places decide which jobs rank first.
4. **Automation**: how far getafjob may go on its own (see below).
5. **Matches**: the first ranked jobs. They show up while the jobs are rated.

## Find jobs

**Jobs** lists every open job ranked for you. Filters, the sort order and the page are part of the address, so a
view can be bookmarked. Open a job to see why it got its score: every reason adds or takes points. Select several
jobs to queue, save, skip or block their employer at once.

## Apply

- **Apply now** on a job shows the values that would be filled, each with its source. Starting it opens the run
  in **Live**, where you can watch the browser fill out the form.
- **Needs input** collects runs that wait for you: a question the form asks and your profile does not answer, or a
  check only a human can do. Answer, and the run goes on. Answers you save are used again next time.
- **Approvals** holds filled applications before they are sent. Review the values and their sources, then approve
  one or several at once, or decide not to send.

Nothing is sent twice, every value has a source, and a click without effect is retried once and then parked.

## Automation levels

| Level | What happens |
|---|---|
| Suggest only (default) | getafjob finds and ranks jobs; you start every application yourself. |
| Fill in and approve | getafjob fills out forms on its own and sends only after your approval. |
| Autonomous by rule | getafjob sends on its own when every field has a source and no rule asks for you. |

Nothing is queued on your behalf until you choose another level. Change it under Settings, Automation.

## Track

**Applications** lists everything you applied to, grouped by what needs attention. **Pipeline** shows the same
applications as columns from saved to offer; drag a card or use its menu to move it. Notes, salary and interview
dates live on the card.

## Stay informed

The bell at the top shows what changed: sent applications, questions, replies and status suggestions from your
mailbox. It updates on its own. **Activity** lists everything by day. Mail, web push and ntfy follow your language;
choose the channels, quiet hours and a daily summary under Settings, Notifications.

## Your data

Download all your data or delete your account under Settings, Account and security. Deleting removes every
document, answer and application at once and cannot be undone.

## For admins

The **Admin** group appears for admins only. **Users** creates invitation links (no mail is sent), changes roles,
signs people out everywhere and deletes accounts; every action is recorded on the same page. **System** shows the
version, the database and which channels are on.
