# RFC 002: Multimodal Vision Pipeline Limitations & Future Plan
**Status:** Draft / Post-v0.4.0 Analysis
**Context:** Current implementation of PDF image extraction and semantic indexing.

## 1. Executive Summary
With release v0.4.0, KektorDB introduces a multimodal "Vision" pipeline that extracts images from PDFs, generates textual descriptions via LLM (e.g., Llama 3.2 Vision), and indexes them as searchable vectors.
While functional, the current implementation sacrifices **positional precision** for **architectural simplicity** (avoiding complex CGO dependencies).

This document analyzes the limitations of the current approach and proposes a technical roadmap to resolve them in future versions.

---

## 2. Current Architecture & Flow

### 2.1 The Extraction Process
1.  **Text Extraction:** Uses `ledongthuc/pdf` to extract all text into a continuous string.
2.  **Image Extraction:** Uses `pdfcpu` to extract all image objects into a temporary directory (and then persists them to `assets/`).
3.  **Vision Analysis:** Each image is sent to the LLM, which generates a description.
4.  **Injection:** Descriptions (and Markdown links to images) are **appended to the end** of the full document text.
5.  **Chunking:** The text (Document + Appended Descriptions) is split into chunks.

---

## 3. Known Limitations (Technical Debt)

### 3.1 Loss of Spatial Context (The "Appendix" Problem)
**Issue:** All images are moved to the end of the document ("Append-Only").
*   *Example:* If a PDF has `Text A` -> `Image A` -> `Text B`, in KektorDB it becomes `Text A` -> `Text B` -> `Description Image A`.
**Consequence:** The `prev/next` graph links connect the image to the last paragraph of the document, not to the paragraph that introduced it. This reduces RAG context quality if the image is crucial for understanding adjacent text.

### 3.2 "Vector vs Raster" Blindness
**Issue:** `pdfcpu` extracts only raster images (bitmaps: JPG, PNG). Many technical PDFs (e.g., datasheets, scientific papers) use vector graphics (SVG/PDF draw commands).
**Consequence:** These charts are completely ignored by the Vision pipeline. The text inside them is extracted (often in a disorganized way), but the visual "shape" is lost.

### 3.3 LLM Dependency for Rendering
**Issue:** The injection of the image into the final chat (`![img](url)`) relies on the LLM deciding to copy the Markdown tag into its output.
**Consequence:** With small models (< 7B) or those not well-instructed, the tag is often lost or transformed into plain text, making the image invisible to the end user.

---

## 4. Proposed Solutions (Future Implementation Plan)

### 4.1 Solution: Placeholder Injection (Context Fix)
*Goal: Restore correct image positioning in the text flow.*

**Technical Strategy:**
Instead of extracting text and images in two separate, blind passes, we must use a parser that returns an ordered list of mixed elements.

1.  **Low-Level Parsing:** Stop using `ledongthuc/pdf` (high-level text) and go down to the layout level.
2.  **Token Stream:** Build a token stream that includes `TOKEN_TEXT` and `TOKEN_IMAGE_REF`.
    *   *Stream:* `[Text: "Here is the chart:"]`, `[ImageRef: img_123.jpg]`, `[Text: "As you can see..."]`.
3.  **Inline Replacement:** During the Vision Pipeline, replace `[ImageRef]` with the generated description and Markdown link, preserving position.

**Challenge:** Requires a much more advanced Go PDF library (e.g., `unidoc` - beware of license, or a forked `rsc/pdf`) capable of providing Y-coordinates of objects on the page.

### 4.2 Solution: Page-to-Image Rendering (Vector Fix)
*Goal: Capture vector graphics and complex tables.*

**Technical Strategy:**
Instead of extracting image objects, render the entire PDF page as an image (screenshot).

1.  **Rendering:** Convert `page_1.pdf` -> `page_1.png`.
2.  **Vision:** Send the entire page to the LLM.
3.  **Prompt:** *"Extract the text and describe the charts present on this page."*

**Challenge (Critical):** There are no performant **Pure Go** libraries to render PDFs to images. It requires `CGO` with `libpoppler` or `mupdf`. This breaks the portability of the static binary (cross-compilation becomes a nightmare).
*Decision:* This path should only be taken if we accept having separate/complex builds or an optional sidecar service.

### 4.3 Solution: Proxy-Side Injection (UI Reliability Fix)
*Goal: Ensure the image always appears in the chat, regardless of LLM intelligence.*

**Technical Strategy:**
Move the visualization responsibility from the LLM to the Proxy.

1.  **Metadata Flag:** When the DB returns a chunk that is an "Image Description", the Proxy flags it.
2.  **Stream Interception:** The Proxy forwards the LLM response to the user.
3.  **Append:** Once the LLM stream is finished, the Proxy forcibly appends an extra Markdown block: `\n\n**Reference:** ![img](url)`.

**Challenge:** Requires managing the HTTP stream (SSE) in the proxy to inject extra data packets at the end. Feasible but delicate.

---

## 5. Recommendation for v0.5

It is recommended to prioritize **Solution 4.3 (Proxy-Side Injection)** because:
1.  It solves the most visible problem (missing images).
2.  It is pure Go code in the Proxy, with no external dependencies.
3.  It improves UX immediately.

The positional problem (4.1) is acceptable for now, as the semantic graph (`mentions`) mitigates the loss of sequentiality.