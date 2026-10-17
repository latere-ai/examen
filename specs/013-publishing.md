---
title: "Publishing: the one definition of sharing, from share grants to links and listings; reports, benchmarks and datasets published as immutable snapshots with an evidence level and a secret scan; evidence served through Arca read-only links; reads without sign-in; benchmark submissions and a results table of ordered entries with simultaneous rank intervals and a comparison with a reference entry; withdrawal, pause and attribution"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
  - specs/007-measurements-and-evidence.md
  - specs/008-analysis.md
  - specs/010-reports.md
affects: [internal/publish/, internal/analysis/, internal/httpapi/, internal/access/, cmd/examen-scan/, api/]
effort: large
created: 2026-10-17
updated: 2026-10-18
author: changkun
---

# Publishing

## Overview

People share what Examen measured so that others can read it, run it
again or build on it: an experiment's comparisons, a suite others can
run with the results submitted to it, the trials behind a study. This
spec is the one definition of sharing in Examen. It fixes the three
ways to share, in the words people already meet for anything shared:
**only people invited**, which is the share
grant of one owner to another; **anyone with the link**; and **listed**
for everyone, which only a listing the installation approves can do.

What is shared without a grant is never a live object. It is a
**publication**: a report, a benchmark or a dataset, published as
immutable versions, each a snapshot with a digest. A version carries
the evidence level its owner chose, and before it publishes a secret
scan runs in a Cella sandbox over its snapshot and any trial content it
carries. Evidence is served for download only, through Arca read-only
links that Examen creates for exactly the published subset and revokes
when the version is withdrawn. A reader who does not sign in reads a published version
through the public routes, by its link or its public id, and the
authorizer is asked about every such read with no subject.

The objects are spec 002's (publication, publication version,
submission, share), the routes and errors spec 003's, the actions spec
004's. This spec fixes what they do.

## Design

### Three ways to share

| Way | Visibility | Who reads | How |
|---|---|---|---|
| Only people invited | `private` | the owner and the owners it invites | a share grant per owner (below); every object Examen keeps is `private` to its owner until shared |
| Anyone with the link | `unlisted` | anyone who holds the publication's link, without signing in | the link token (Links, below) |
| Listed | `public` | anyone, and the publication appears in the listings | an approved listing (Listing, below) |

`private` is the default for every owner, a person or an organization.
A share grant reaches live objects; a link and a listing reach only
published versions. The words are spec 002's `visibility` values, and
an installation that shares other things with the same three keeps one
vocabulary for its people.

### Only people invited: share grants

A share (spec 002) grants one owner reading, and for a suite or a
benchmark using, of one object. An owner shares a suite it owns, a
baseline it pinned, an experiment or a publication through the routes
of spec 003. A share names a suite or baseline by name, so it covers
every record of that name, those made after it included, until it is
removed.

| Share | The grantee may | The grantee may not |
|---|---|---|
| a suite | read every version of the suite, and every task, metric, grader and taxonomy those versions use; name any version in its own experiments, whose work is then its own, on its own budget; pin baselines and apply bars of its own on the suite | apply a version under the name, share it on, or read the sharer's experiments, candidates, baselines or bars |
| a baseline | read the baseline's records; name one as its experiment's `reference`, so its comparisons are computed against the sharer's pinned cells | read the sharer's experiment, trials, measurements or evidence: a comparison carries the baseline side's summaries alone (spec 003) |
| an experiment | read the experiment, its grid, cells, trials, measurements and comparisons | change, cancel, retry, re-grade, delete or share it; evidence bytes are read only as Arca allows |
| a publication | read every published version through the signed-in routes, whatever its visibility; for a benchmark, run its suite and submit to it as its `submissions` allow | change, withdraw, list or share it, or read a version that never published |

A baseline is used only by an experiment of the same suite, so a
baseline share is useful beside a share of its suite. Evidence stays in
the sharer's Arca space, and granting it is Arca's own act. Removing a
share stops new references and new reads once any cached allow has
lapsed (spec 004). An experiment that already pins a version of a
shared suite or a shared baseline keeps it: reads of the objects it pins
carry `pinned: true`, and its cells, comparisons and reports keep
reading what they pinned, so a result stays reproducible.

### Publications and their versions

A publication is one of three kinds, each a snapshot of something its
owner already holds:

| Kind | A version snapshots | Its `source` |
|---|---|---|
| `report` | an experiment's comparisons, read-only: a report's frozen document (spec 010) and its released narrative, with the trial content of its evidence level | a report of the owner's |
| `benchmark` | a suite version others can see and run, with the results submitted to it | a suite digest of the owner's |
| `dataset` | a versioned dataset export (spec 007), with license and citation | a `complete` dataset export of the owner's |

**Publishing** creates the publication with its first version (`POST
/publications`), and each later change publishes a new version (`POST
/publications/{id}/versions`). A version is immutable: its `snapshot`
and `digest` never change, and it is never edited, only withdrawn. A
reference to a publication reads its **latest** version, the newest in
state `published`, unless it pins one by `number`.

A version moves `preparing` → `scanning` → `published` (spec 002).
While `preparing`, the `publish` job builds the snapshot and, for a
version that carries files, copies them into the version's folder,
`files/evals/publications/<publication id>/<number>/` in the owner's
Arca space (spec 007), each copy with Arca's copy and its digest
checked against its evidence ref. Every version then passes
`scanning`, whatever its evidence level, since its snapshot's JSON is
scanned too (below). At `published`, Examen creates the version's Arca
link when it has a folder, and the snapshot is fixed. A copy whose digest does not match, a core that refuses, and
any other failure end the version `failed` with an error object; a
scan that finds a secret ends it `blocked`. Neither ever publishes,
and the owner publishes a new version instead, so a version number
names one content for its whole life.

The `publish` job acts for the owner on behalf of the version's
`published_by`, with owner tokens of the purpose `publish` (spec 004).
A minter that refuses fails the version with its reason. Every job is
idempotent, and a restarted examend resumes a version from its state.

A publication is a snapshot and stands apart from what it was
published from: deleting the experiment, the report or the export, or
the evidence they referenced, leaves every published version and its
folder as they are, and only a withdrawal stops a version from being
read.

### The snapshot

The snapshot is one JSON document, format `examen.publication/v1`,
whose canonical JSON (spec 002) is hashed into the version's `digest`.
Every snapshot holds `format`, the publication's `kind`, `name`,
`title` and `description` as they stood when the version published,
the version's `number` and `evidence_level`, and its `attribution`.
Then, by kind:

- **A report**: the report's `document` (spec 010) and its released
  narrative, `null` when none is released; a draft never. At `outputs`
  and `full`, `trials`: one entry per finished trial of the report's
  experiment, `{trial, task, candidate, index, outcome, values,
  evidence}`, `values` the trial's current values (spec 007) and, at
  `full`, their rationales, and `evidence` the trial's published
  evidence objects, each `{kind, name, path, digest, size}` with `path`
  relative to the version's folder, or `{kind, name, excluded: true}`
  for one the owner left out. A baseline's trials, which belong to
  another experiment, are never in it.
- **A benchmark**: the suite version and every task, metric and grader
  it pins, its taxonomies at their current records, and its primary
  metric. A task's references are named by name and digest alone: their
  content, an inline reference's text and an Arca or Origo path, is
  never in a snapshot, so a reader of a benchmark never reads its
  answers. An Arca input is named by name, digest and size, without its
  path in the owner's space. A benchmark is open, not held out: every
  experiment run through it copies its references and its checks'
  files into the runner's own folder for its checks (spec 005), so
  whoever runs it can read them, and its entries are trusted only as
  far as their runners, which every surface that shows a benchmark
  says. Held-out grading, where the benchmark's owner or the
  installation runs the references and checks and never copies them to
  the runner, is a later feature.
- **A dataset**: `{name, version, description, license, citation,
  format, evidence_level, suites, experiments, records, files}`, its
  `version` the publication version's number, `format` the export's
  record format, `suites` and `experiments` what the export's records
  came from, and `files` each file of the version's folder with its
  path, digest, size and line count.

### Evidence levels

The owner chooses each version's evidence level. It decides which trial
content a version carries:

| Level | Carries | Evidence kinds of spec 007 |
|---|---|---|
| `aggregates` (the default) | summaries, verdicts, intervals, confounds and counts; no trial content | none |
| `outputs` | also each trial's declared outputs, workspace files and each check's result, and its current values | `output`, `file`, `observed`, and of `check_output` its `result.json` alone |
| `full` | also transcripts, final messages, request records, judge rationales and each check's whole stdout and stderr | also `transcript`, `final_message`, `requests`, `judge_rationale`, and `check_output`'s `stdout.txt` and `stderr.txt` |

A check's stdout and stderr can echo the references it compared and
much of the workspace it read, so they publish only at `full`; its
`result.json` holds the values the check reported. A version at
`outputs` or `full` needs the installation to declare its file store
download-only, `EXAMEN_FILESTORE_DOWNLOAD_ONLY` (spec 011): every link
read answers `Content-Disposition: attachment`,
`X-Content-Type-Options: nosniff` and `Content-Security-Policy:
sandbox`, so a file a model wrote, often a web page, is downloaded and
never rendered on the file store's origin; without the declaration the
version is refused `422 file_store_not_download_only`. A benchmark is
published at `aggregates`: its snapshot holds definitions, never a
trial's content. A dataset's records are written
into its folder reduced to its level: at `aggregates`, with every
measurement's value and without rationales or evidence refs; at
`outputs`, with the refs of the kinds above, pointing into the folder;
at `full`, with every rationale and ref. The side files and the
manifest of spec 007 come with them, with `dataset.json`, the
snapshot's dataset member, and `CITATION.cff`.

The evidence and files one version copies are at most
`PUBLICATION_EVIDENCE_MAX_BYTES`, summed from the sizes their refs and
the export's manifest record, so a version over it is refused before
anything is copied.

### The secret scan

Before any version publishes, a secret scan runs over everything it
will publish: its snapshot's JSON at every evidence level, which for a
benchmark holds the task, grader and HTTP runner definitions with
their `env`, headers and inline inputs, and every file of its folder.
Examen creates a Cella sandbox as the owner, with the purpose
`publish`, from Examen's secret scanner image, `examen-scan`, whose
digest the examend build pins as it pins the trial driver's (spec
011), with network `none`. The scanner reads text in UTF-8 and UTF-16,
opens tar, zip and gzip containers, a directory output among them, and
scans every file inside, and reads any other file as the runs of
printable characters it holds; a file it cannot read in one of these
ways, an archive it cannot open or an encrypted one among them, is a
finding on that file, so nothing is published unread. Examen writes
the snapshot, as `snapshot.json`, and the version's folder into the
sandbox through Cella's file routes, each file read from the owner's
Arca space with its digest checked, runs
`examen-scan` over it through Cella's exec route under
`SECRET_SCAN_TIMEOUT`, reads the findings it prints, and deletes the
sandbox. Examen executes no scanner code itself.

The scanner prints one JSON object, `{findings}`, each finding `{path,
rule, line}`: the file, the rule of the scanner's pinned rule set that
matched, and the line. It never prints the matched text, and Examen
never stores or answers it. The findings and the scanner's image
digest are the version's `scan`. A finding ends the version `blocked`:
no Arca link is created, and nothing of it is readable but by its
owner. The owner publishes a new version whose `exclude` names each
file to leave out, which is then not copied, and its snapshot marks it
`excluded`; a finding in the snapshot itself names `snapshot.json`,
and the owner changes the definition it names and publishes again. A
scan that cannot run, or does not end within `SECRET_SCAN_TIMEOUT`,
fails the version. Every version needs Cella configured, and one at
`outputs` or `full` Arca too; without them it is refused `422
core_not_configured`.

### Arca links for published evidence

Published files are served by Arca, never by Examen. When a version
with a folder publishes, Examen creates one read-only Arca link of kind
`link` on exactly that folder through Arca's link API (`POST
/v1/shares/links`), as the owner with the purpose `publish`, and keeps
its id as the version's `evidence_link`. The link's token, which Arca
answers once, is held in the version's row for building links and
leaves Examen only inside the links of a read; it is in no log line,
event or error. A public read answers each evidence object with its
link, Arca's file route for that path under the link
(`/v1/shares/links/{token}/files/{path}`) on `EXAMEN_ARCA_PUBLIC_URL`
(spec 011).

Examen mints no file link of its own. Its link tokens (Links, below)
address a publication's snapshots, not files.

A benchmark version holds no link to its owner's inputs. The `prepare`
job of each experiment run through it mints the links it reads, for
that run alone: as the benchmark's owner on behalf of the version's
`published_by`, one link on each Arca input and reference of the
suite's tasks and on each check's `files`, each expiring after
`INPUT_LINK_TTL`, kept in Postgres only while the job runs, revoked
when it ends, never answered and scrubbed from telemetry (specs 005
and 011). They copy a task's inputs, references and check files into
the runner's experiment's folder, since the runner, whose identity the
experiment carries, cannot read the benchmark owner's Arca space. An
Origo input is read at its commit as the runner, and inline text comes
with the task.

When the publication's link token is replaced, or the publication is
made `private`, Examen replaces each published version's Arca link
during that request: it revokes the old link and creates a new one, so
a file link copied while the publication was readable by its link
stops resolving with it.

Every link of a version is revoked through Arca's link API (`DELETE
/v1/shares/links/{id}`) when its owner withdraws it, during the owner's
request. A pause revokes nothing: while a publication is paused, Examen
answers no public read of it and lists no link of it, and requires the
installation to refuse its link reads at its file store, whose link
reads ask the installation's authorizer (Arca's do), so a resume,
which an administrator takes with no owner present, needs no owner
token. A revoke
that fails is retried until Arca accepts it, and the version reads as
withdrawn or paused from the moment the request commits.

### Links

Each publication has one link token, 256 bits from `crypto/rand`
rendered base64url, minted at create. The token is the capability:
whoever holds it reads the publication's published versions, without
signing in, while its visibility is `unlisted` or `public` and it is
not paused. `POST /publications/{id}/link` replaces it, and the old
token stops resolving at once. The token is answered, in the
publication's `link`, only to a caller the authorizer allows
`publication.update`; it is never in a list, an event, a log line, a
span or an error, and the public routes record their route pattern,
never their path, which holds it.

The link a person shares is `<EXAMEN_PUBLIC_URL>/public/<token>`, a page
a console renders from the public reads, and is absent without
`EXAMEN_PUBLIC_URL`. A publication made `private` keeps its token,
which resolves again if it is made `unlisted` and has not been
replaced.

### Listing

`public` comes only through a listing. No request sets `visibility` to
`public`: `PUT /publications/{id}/listing` asks for it, and Examen asks
the authorizer `publication.list`, which an authorizer allows only for a
listing its installation approved, with its own review, where it
reviews what its people list elsewhere. Examen stores the visibility
and asks the question, and builds no review of its own. `DELETE
/publications/{id}/listing` ends a listing, asked the same way, and
returns the publication to `unlisted`; an installation that ends a
listing on its side calls it. While listed, a publication's visibility
cannot be changed by `PATCH`, so every end of a listing passes through
the question the installation answers.

A benchmark is listed only with `submissions` `reviewed` and at most
one accepted entry per owner besides the benchmark's own, so every
entry of a public table was looked at by its owner and no submitter
fills it: a listing otherwise is `409 benchmark_not_reviewed` or `409
owner_has_entry`, and while it is listed its `submissions` stays
`reviewed` and a second entry of one submitter is not accepted (spec
003).

Who may make a publication `unlisted` is the authorizer's too: it sees
`visibility` on `publication.create` and `publication.update`, and an
installation whose organizations keep their items closed by default
denies `unlisted` to an organization's members until its administrator
allows it. Under the owner policy, owners are people, `unlisted` is
allowed and nothing is ever listed (spec 004).

### Reading without signing in

The public routes of spec 003 serve published versions, read-only, to a
reader who sends no bearer: a publication by its link token, which
resolves while it is `unlisted` or `public`, or by its public id, its
`pub_` id, which resolves while it is `public`; and the listings, which
hold every `public` publication that is not paused. A read answers the
snapshot, the version list and, for a version with a folder, the
evidence objects with their Arca links; for a benchmark, its results
table and the comparison of two of its entries. It never answers a live object: no experiment, trial, measurement
or comparison is reachable from it, and a figure's source in a report's
document reads as plain text.

Before answering, Examen resolves the key, then asks the authorizer
`publication.read` with no subject, the publication's id, its
`visibility`, the version and `via`, so an installation can refuse
public reads per owner, per kind or altogether. A key that resolves to
nothing and a denied read answer the same `404 publication_not_found`,
byte for byte, and no answer or log line carries the key. Reads are
rate limited per client address at `PUBLIC_REQUESTS_PER_MINUTE` per
replica (spec 003).

### Benchmarks

A benchmark publishes a suite version for others to run and collects
the results run on it.

**Running.** Whoever can see a published benchmark may run its suite:
an experiment names the suite's digest with `benchmark`, the benchmark's
link token or public id (spec 003), and `suite.use` reaches the
authorizer with `publication` and `published` (spec 004), which the
owner policy allows. A benchmark shared with an owner is run the same
way, with `shared` true. The experiment is the runner's own, on its
own budget; its `prepare` job reads the benchmark owner's Arca inputs,
references and check files through links it mints for that run (Arca
links for published evidence, above), and reads the suite's taxonomies
as they stand. The runner's experiment then holds the references and
the checks' files, so a benchmark is open, not held out (The
snapshot, above).

**Submissions.** A submission (spec 002) enters one candidate of a
report publication version in a benchmark version's results table. The
report's experiment must have run that exact suite digest, and the
report must be `published`; for an `unlisted` or `public` benchmark
the report must be `unlisted` or `public` too, so every entry a reader
sees leads to the report behind it. The benchmark's owner sets
`submissions`:

| `submissions` | Who may submit | A new submission is |
|---|---|---|
| `owner_only` (the default) | the benchmark's owner | `accepted` |
| `reviewed` | anyone who can see the benchmark | `pending` until the owner accepts or rejects it; the owner's own is `accepted` |
| `open` | anyone who can see the benchmark | `accepted` |

The owner may reject an accepted submission later, and the submitter
may withdraw its own; a withdrawn report version withdraws its
submissions. At submission Examen copies the entry's figures from the
report version's document into the submission's `entry`, so an entry
never changes under a reader: the candidate's attribution; its name,
kind, digest, model and reasoning, and for an `external` candidate its
declared `harness` and `version`; its suite-level summary of the
benchmark's primary metric, with its interval, denominator and
coverage, as the report's grid holds it; its confounds that a side
carries alone, `missing`, `self_reported`, `observed.model` and
`sent.model`, with their counts; whether its provenance is declared,
true for an `external` candidate; whether its summary reads
self-reported values; and, for a primary metric that reads an open
vocabulary, the taxonomy record its labels were read at. Beside these figures Examen copies the entry's
`values` from the report's experiment, as the report's figures read
them (spec 007): for each task of the suite, the task's weight and the
primary metric's value of each of the candidate's finished trials,
with the cells' tuple fields outside the candidate, the counts spec
008's confounds read, and the entry's `window`, when its trials ran
(spec 002). A value is a number or a boolean, or for a cell-level
primary metric the canonical label of its `of` metric that the
statistic is computed from; no output, transcript, rationale or other
trial content is copied, whatever the report's evidence level. A report
whose experiment was deleted can no longer be submitted, since its
values cannot be read (spec 003). An entry's figures are its own
report's, and entries meet only in the tests of the results table,
over the values each copied.

A benchmark's primary metric is of level `trial` or `cell` and not a
`label`, since its results are ordered by one figure; a suite whose
primary metric is not is refused at publish, `400 invalid_field` on
`source`.

**The reference.** A benchmark version's `reference` is the accepted
entry every other entry is compared with: the one the benchmark's owner
names (`PATCH /publications/{id}`, spec 003), and otherwise the
owner's own accepted entry of the lowest submission id. When the named
entry is rejected or withdrawn, the default applies again. With no
accepted entry of the owner's and none named, the version has no
reference, and no entry carries a comparison with one.

**The results table.** For a benchmark version, its accepted entries,
at most `MAX_BENCHMARK_ENTRIES`: an acceptance beyond it, at submission
or by the owner, is refused `409 benchmark_full` (spec 003). The table
never shows a bare ranking, and never hides an order the data does
show by showing none:

1. **Ranked and unranked.** An entry is unranked when any finished
   trial of a task of weight above zero in its `values` has a missing
   or self-reported value of the primary metric, or when its tasks of
   weight above zero that hold at least `MIN_TRIALS_PER_TASK_IN_SUITE`
   valued trials hold less than `MIN_SUITE_COVERAGE` of the suite's
   weight. Of the others, taken in spec 008's order, an entry is ranked
   while the tasks every ranked entry covers, the **common tasks**,
   still hold at least `MIN_SUITE_COVERAGE` of the weight, and unranked
   when it would shrink them below that. An unranked entry is listed
   after the ranked ones, in the same order, with why, and with no rank
   interval and no comparison with the reference. Declared provenance
   alone does not unrank an entry; it is marked.
2. **Order.** Ranked entries are ordered by their suite-level summary
   of the primary metric over the common tasks, which the table
   computes, in the primary metric's direction, best first: descending
   for `higher_is_better`, ascending for `lower_is_better`, and
   descending for `none`, which the table says has no better end. Ties
   keep submission id order. An entry's `order` is its place, from 1.
   It is a display order and never shown as a rank by itself.
3. **Rank intervals.** Each ranked entry shows `rank: {low, high}`,
   which hold together across the table at `CONFIDENCE_LEVEL` (spec
   008, Benchmark entries): every two ranked entries are tested with
   the task-stratified test of the primary metric's suite-level result
   over the common tasks, the stratified permutation of the metric's
   statistic with the tasks fixed and weighted as declared, and Holm's
   procedure adjusts all K(K − 1) / 2 pairs together at
   1 − `CONFIDENCE_LEVEL`, K the number of ranked entries. `low` is 1
   plus the number of entries significantly better than it, and `high`
   is K less the number significantly worse. An entry apart from
   neither of two neighbors that are apart from each other spans both.
   For direction `none`, better is the higher value, and the table says
   the interval is a place in that order, not a rank of better and
   worse.
4. **Against the reference.** Each ranked entry but the reference
   itself carries `vs_reference`, spec 008's pair result of the entry
   against the reference entry, from the same Holm-adjusted family as
   the rank intervals: `delta`, the entry less the reference, its
   `interval`, `p`, `h`, `verdict` (`improved`, `regressed`,
   `within_noise` or `insufficient`, and `changed` in place of the
   first two for direction `none`), `reason` and `confounds`. The
   verdict follows `h`; the unadjusted `p` is shown in the reason as
   text and never as the verdict. The confounds are the tuple fields
   outside the candidate that differ, the layer confounds (a served
   model, a harness's image or a service version that differs),
   `incomplete`, `declared` for an `external` side, `window` when the
   two entries' trials ran in time windows more than
   `WINDOW_TOLERANCE` apart, and `taxonomy` when their labels were read
   at different taxonomy records.
5. **Two entries on demand.** `GET /publications/{id}/results/compare`
   and its public twin answer the stored pair result of any two
   accepted entries of a version with the members of 4, its verdict
   following `h`, and say that it is one pair of the table (spec 003).

The work queue computes the table when the version's accepted entries
or its reference change, and stores it with the version (spec 002)
with its inputs digest and seed, deterministic as spec 008 requires;
the results routes answer the table as last computed. Each entry is
marked when its provenance is declared, and names its own confounds in
its row. The table says, beside it, that the benchmark is open and an
entry is trusted only as far as the one who ran it.

The table does not group entries whose intervals overlap, for the
reasons spec 008 gives: overlap is not a test, does not chain, ignores
that the entries ran the same tasks, and ignores how many pairs a table
compares.

### Datasets

A dataset version names a `complete` dataset export of the owner's and
gives:

| Member | Rule |
|---|---|
| `license` | required: an SPDX license identifier or expression, checked against the SPDX license list the Examen release pins, else `400 invalid_field` |
| `citation` | optional: Citation File Format 1.2.0 fields the owner gives, `authors`, `abstract`, `keywords`, `repository-code` and `message`, at most `PUBLICATION_TEXT_MAX_BYTES` as canonical JSON; Examen fills `cff-version`, `title`, `version`, `date-released`, `license` and, with `EXAMEN_PUBLIC_URL`, `url` |

Its name and description are the publication's, its version the
publication version's number, its schema version the export's record
format, and its evidence level the version's. The version's folder
holds the records at that level, the side files, `dataset.json`,
`CITATION.cff` and the manifest, each downloadable from its Arca link
and listed by the link's listing route. A DOI is out of scope.

### Withdrawal and pause

**Withdrawal** is the owner's (`publication.withdraw`). A withdrawn
version stays in the publication's version list, with `withdrawn_at`
and `withdrawn_by`, so a link that pinned it answers `410
publication_withdrawn` and a page says it was withdrawn; every Arca
link of the version is revoked; a report version's submissions are
withdrawn; a benchmark version's results table is no longer served,
and experiments already run through it keep the copies they made. The
publication's latest version moves to the newest one still
`published`, and with none left the publication answers `410
publication_withdrawn`. Its folder stays in the owner's space, as
exports and report bundles do (spec 007), for the owner to delete.

**Pause** is an administrator's (`publication.pause`), as for anything
an installation lists: `POST /publications/{id}/pause` with a reason
sets `paused`, every public read of the publication answers `410
publication_paused`, the listings leave it out, and the installation
refuses its link reads at its file store, while Examen revokes nothing
(above). Its visibility does not change, and its owner still reads it
through the signed-in routes. `POST /publications/{id}/resume` clears
`paused`, and its links answer again. Under the owner policy no
one pauses (spec 004).

### Attribution

A published version names its owner: the person's profile name or the
organization's name, kept in the version as `attribution` and in every
snapshot. Examen holds no profile, so the name comes from the
authorizer's `attribution` limit on `publication.create`, or on a
`publication.update` that publishes a version (spec 004); an
authorizer's name replaces any the request gave. Without it, the
request's `attribution` is used, and without that, the owner as
rendered. A version keeps the name it was published with; a renamed
owner's next version shows the new one. A benchmark entry shows its
report version's attribution.

### Limits

The constants of spec 002 this spec holds:
`PUBLIC_REQUESTS_PER_MINUTE`, `PUBLICATION_LINE_MAX_BYTES`,
`PUBLICATION_TEXT_MAX_BYTES`, `PUBLICATION_EVIDENCE_MAX_BYTES`,
`SECRET_SCAN_TIMEOUT`, `MAX_BENCHMARK_ENTRIES`,
`MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`,
`CONFIDENCE_LEVEL`, `WINDOW_TOLERANCE` and `INPUT_LINK_TTL`.

## Not in this spec

The objects and their fields (002), the routes, their answers and
errors (003), the actions, the owner policy and the attribution limit
(004). Rendering pages, share images and a share dialog: a console or
an installation renders them from the public reads. Reviewing a
listing, which the installation does. A DOI for a dataset. A bare
ranking: the results table reports rank intervals adjusted across the
table, and the statistics behind them are spec 008's. Held-out grading
of a benchmark, where its references and checks never reach a runner,
which is a later feature. Comparing two
entries per task or on another metric than the primary one, which a
comparison of spec 008 does within one experiment.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| A published version's snapshot and digest never change; a change publishes the next version; a link resolves the latest `published` version, or the one it pins | `publish.TestVersionsAreSnapshots` |
| Deleting the experiment, report or export a version came from leaves the version, its snapshot and its folder readable | `publish.TestSnapshotOutlivesItsSource` |
| A report version at `aggregates` carries no evidence object; at `outputs` it carries exactly the outputs, workspace files, each check's `result.json` and the observed records of the report's experiment's finished trials; at `full` also transcripts, final messages, request records, judge rationales and each check's stdout and stderr; never a baseline's trials | `publish.TestEvidenceLevels` |
| A version at `outputs` or `full` on an installation without `EXAMEN_FILESTORE_DOWNLOAD_ONLY` is refused `422 file_store_not_download_only` before anything is copied | `publish.TestDownloadOnlyRequired` |
| A benchmark snapshot holds no reference's content or Arca path, and a canary planted in an inline reference is in no snapshot or public answer | `publish.TestBenchmarkHidesReferences` |
| A dataset version's records drop rationales and evidence refs below `full` as the table says, and its license is required and checked | `publish.TestDatasetLevels`, `publish.TestLicenseRequired` |
| Every version is scanned in a Cella sandbox from the pinned scanner image before any link exists, its snapshot's JSON at every evidence level and its files at `outputs` and `full`; a secret planted in a task's `env` or an HTTP runner header of a benchmark, or in a file, ends it `blocked` with the file and rule and never the text; a file the scanner cannot read is a finding; a new version excluding the file publishes and marks it excluded | `publish.TestSecretScanBlocks`, `publish.TestSnapshotIsScanned`, against the stub Cella |
| A published version's files are served only through one Arca link on its folder, created as the owner; withdrawing revokes it; replacing the link token or making the publication `private` replaces it, and a file link taken before stops resolving; a pause revokes nothing; Examen serves no file bytes | `publish.TestEvidenceThroughArcaLinks`, `publish.TestLinksFollowVisibility`, against the stub Arca |
| A benchmark version holds no input link; an experiment run through it copies the owner's Arca inputs, references and check files through links its `prepare` job mints for that run, which expire after `INPUT_LINK_TTL`, are revoked at the job's end and are in no answer | `publish.TestBenchmarkInputLinks` |
| No request sets `public`; `publication.list` is asked for a listing and its end; under the owner policy nothing is listed or paused; a benchmark is listed only `reviewed` with one accepted entry per owner besides its own | `publish.TestPublicOnlyThroughAListing`, `publish.TestListedBenchmarkIsReviewed` |
| A public read answers without a bearer, by link token while `unlisted` or `public` and by public id while `public`; it asks `publication.read` with no subject; a denied read and an unknown key answer alike; no answer reaches a live object | `publish.TestPublicReads` |
| The link token is in no list, log line, span, event or error, and replacing it stops the old one | `publish.TestLinkTokenStaysSecret`, with a canary |
| A withdrawn version answers `410 publication_withdrawn` and moves the latest to the newest published one; a paused publication answers `410 publication_paused` and leaves the listings | `publish.TestWithdrawAndPause` |
| A submission needs a published report of the benchmark's exact suite digest, follows `submissions`, and copies its entry and its values at submission and no trial content, whatever the report's evidence level; a report whose experiment was deleted is refused; a withdrawn report withdraws its submissions | `publish.TestSubmissions` |
| The results table orders ranked entries over the common tasks in the primary metric's direction; gives each the rank interval `[1 + B, K - W]` from the pair tests over the common tasks adjusted by Holm's procedure across all K(K − 1) / 2 pairs, an entry between two neighbors that are apart spanning both; lists after the ranked ones, with why and without a rank interval or verdict, the entries with a missing or self-reported primary value and those below `MIN_TRIALS_PER_TASK_IN_SUITE` or `MIN_SUITE_COVERAGE` alone or with the common tasks; marks declared provenance; refuses an acceptance past `MAX_BENCHMARK_ENTRIES` with `409 benchmark_full`; and is recomputed when an entry is accepted, rejected or withdrawn | `publish.TestResultsTableRankIntervals`, over committed entries |
| Each ranked entry but the reference carries its pair result against the reference entry, its verdict following `h` and its `p` in the reason, with its confounds, `window` included when the two entries' windows lie more than `WINDOW_TOLERANCE` apart and `taxonomy` when their records differ; the reference is the owner's choice, defaults to the owner's first accepted entry, returns to that default when the named entry leaves, and is absent when the owner has none | `publish.TestResultsTableReference` |
| The compare route and its public twin answer the stored pair result of two accepted entries, the same figures and verdict the reference column shows for that pair, negated when read the other way | `publish.TestResultsPairwise` |
| A version shows the authorizer's attribution over the request's, and keeps it after the owner is renamed | `publish.TestAttribution` |
| After a suite share, the grantee reads the suite's versions and the objects they pin and runs an experiment on it under its own owner and budget, and cannot apply under the name or read the sharer's experiments; after a baseline share, the grantee's comparison is computed against it and no route answers the sharer's trials or measurements; after the share is removed, a new reference is refused and an experiment that pinned the shared version still reads it | `access.TestSuiteAndBaselineShares` |
| After a suite share, the grantee pins a baseline and applies a bar of its own on the suite; after an experiment share, the grantee reads the experiment, its trials and its comparisons and cannot change, cancel, re-grade or delete it; after a publication share, the grantee reads its published versions and cannot change or withdraw it | `access.TestExperimentAndPublicationShares` |
