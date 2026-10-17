# 3. Recognition is one workflow state

Date: 2026-10-17

Status: accepted

## Context

`AGENTS.md` describes the workflow as:

```
Validate -> [Preprocess + Thumbnail] -> OCR -> Extract fields -> Save results
```

Five states, with two of them running in parallel. Implementing it that way
ran into three problems, each of which is about where the work can happen
rather than how it is arranged.

**Tesseract cannot open a PDF.** A PDF has to be rendered to images first,
by poppler's `pdftoppm`. That binary is in the OCR step's container image
and nowhere else, because it is the only step that ships as a container.
With Preprocess as its own state, a PDF has nothing to normalize and no
thumbnail can be made from it: images would take one path through the
workflow and PDFs another, with a visible hole where a PDF's preview should
be.

**A parallel state's branches each need the original.** Preprocess and
Thumbnail both decode the upload, so running them side by side means two S3
reads and two decodes of the same bytes. Decoding is the expensive part of
both.

**A parallel state's output is an array.** Merging two branch outputs back
into one payload needs a `ResultSelector` naming each field by path. That is
exactly the kind of string-keyed plumbing between JSON and Go that nothing
in this project can currently verify — Step Functions Local is
[unsupported by AWS](https://docs.aws.amazon.com/step-functions/latest/dg/sfn-local-test-sm-exec.html),
and the real service is not available until a deployment.

## Options considered

1. **Follow the original shape.** Five states, Preprocess and Thumbnail in
   parallel. Needs a second container image so Preprocess can rasterize, or
   a special path for PDFs; costs two extra transitions and an unverifiable
   merge.

2. **Keep the states but make them sequential.** Removes the array merge.
   Still needs poppler in a second image, or PDFs still have no thumbnail.

3. **Do the rendition work where poppler already is.** One Recognize state
   reads the upload once, rasterizes if the document is a PDF, decodes,
   writes the page and the thumbnail, and recognizes. Extract then stores
   the result.

## Decision

Option 3. The workflow is:

```
Validate -> Recognize -> Extract -> MarkDone
                \-----------------> MarkFailed
```

Saving is part of Extract rather than a state of its own: the two have
nothing to decide between them, and a separate state would cost a cold start
and a transition to pass a value along unchanged.

## Consequences

- **PDFs and images take the same path**, and both get a thumbnail. For a
  PDF it is made from the first rendered page, which is what a preview
  should show anyway.
- **One read and one decode** of the upload instead of three.
- **Four states instead of seven.** Step Functions Standard bills per
  transition and the free tier allows four thousand a month, so this is
  roughly 800 jobs a month rather than 500.
- **One container image.** Only Recognize needs Tesseract and poppler; the
  other four functions stay as `provided.al2023` binaries, which start
  faster and are smaller to ship.
- **Recognize does more than its neighbours.** It is the long step either
  way — OCR dominates the pipeline's runtime — so the extra decoding is
  noise against it. If it ever needs splitting, the seam is between
  producing the renditions and reading them.
- The page cap on PDF rasterization matters more now, since one Lambda
  invocation carries the whole document. `ocr.DefaultMaxPages` is five.
- `AGENTS.md` has been updated to describe this shape. The sentence it
  carried is the one this record overrules.
