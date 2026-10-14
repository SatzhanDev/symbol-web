// variations.js: the "Get Creative Ideas" button (/api/variations, bonus).
//
// The server asks the LLM (or the mock) for creative rewrites of the
// text. They are shown in a modal dialog; choosing one puts its text into
// the input and selects its suggested banner.

import { postJSON, isAbort, replaceText } from "./shared.js";

const input = document.getElementById("text-input");
const button = document.getElementById("get-variations-btn");
const modal = document.getElementById("variations-modal");
const list = modal.querySelector(".variations-list");
const status = modal.querySelector(".modal-status");

let controller = null;

button.addEventListener("click", openIdeas);
modal.querySelector(".modal-close").addEventListener("click", () => modal.close());
// A click on the dark backdrop outside the dialog box closes it too.
modal.addEventListener("click", (event) => {
  if (event.target === modal) modal.close();
});
// Escape closes a <dialog> by itself; in every case stop a pending request.
modal.addEventListener("close", () => controller?.abort());

async function openIdeas() {
  list.replaceChildren();
  modal.showModal();

  const text = input.value;
  if (text.trim() === "") {
    setStatus("Type some text first, then ask for ideas.", true);
    return;
  }

  controller?.abort();
  controller = new AbortController();
  setStatus("Generating ideas…");
  button.disabled = true;
  try {
    const { data, mode } = await postJSON("/api/variations", { text }, { signal: controller.signal });
    showVariations(data.variations);
    setStatus(mode === "mock" ? "Mock mode: canned ideas (no LLM configured). Pick one:" : "Pick one:");
  } catch (err) {
    if (!isAbort(err)) setStatus(err.message, true);
  } finally {
    button.disabled = false;
  }
}

function showVariations(variations) {
  for (const v of variations) {
    // A <button> is focusable and works with Enter/Space, unlike a <div>.
    const card = document.createElement("button");
    card.type = "button";
    card.className = "variation-card";

    const text = document.createElement("span");
    text.className = "variation-text";
    text.textContent = v.text; // textContent: the text comes from an LLM

    const description = document.createElement("span");
    description.className = "variation-description";
    description.textContent = v.description;

    const banner = document.createElement("span");
    banner.className = "badge";
    banner.textContent = v.suggested_banner;

    card.append(text, description, banner);
    card.addEventListener("click", () => choose(v));
    list.append(card);
  }
  list.querySelector("button")?.focus();
}

function choose(variation) {
  const radio = document.querySelector(
    `input[name="banner"][value="${CSS.escape(variation.suggested_banner)}"]`,
  );
  if (radio) radio.checked = true;
  modal.close();
  replaceText(input, variation.text);
}

function setStatus(message, isError = false) {
  status.textContent = message;
  status.classList.toggle("error", isError);
}
