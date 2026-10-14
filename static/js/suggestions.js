// suggestions.js: text suggestions while typing (/api/suggest).
//
// 300 ms after the user stops typing (debounce), and only for at least 3
// characters, the text is sent to the server. The answers appear in a
// dropdown below the input; clicking one (or choosing it with the arrow
// keys and Enter) puts it into the input.

import { postJSON, isAbort, replaceText } from "./shared.js";

const MIN_CHARS = 3;
const DEBOUNCE_MS = 300;

const input = document.getElementById("text-input");
const dropdown = document.getElementById("suggestions-dropdown");

let debounceTimer = 0;
let controller = null; // cancels the previous request when a newer one starts
let items = []; // the suggestions currently shown
let active = -1; // index of the item highlighted with the keyboard

input.addEventListener("input", (event) => {
  clearTimeout(debounceTimer);
  if (event.detail?.programmatic) {
    // The text was replaced by a picked suggestion or variation.
    hide();
    return;
  }
  if (input.value.trim().length < MIN_CHARS) {
    controller?.abort();
    hide();
    return;
  }
  debounceTimer = setTimeout(() => getSuggestions(input.value), DEBOUNCE_MS);
});

input.addEventListener("keydown", onKeyDown);
input.addEventListener("blur", hide);

async function getSuggestions(text) {
  // The user may have left the input during the 300 ms wait.
  if (document.activeElement !== input) return;

  // An older, slower request must not overwrite newer suggestions.
  controller?.abort();
  controller = new AbortController();

  showStatus("Thinking…");
  try {
    const { data, mode } = await postJSON("/api/suggest", { text }, { signal: controller.signal });
    displaySuggestions(data.suggestions, mode);
  } catch (err) {
    if (isAbort(err)) return; // replaced by a newer request
    showStatus(err.message, true);
  }
}

function displaySuggestions(suggestions, mode) {
  items = suggestions;
  active = -1;
  dropdown.replaceChildren();

  suggestions.forEach((text, i) => {
    const option = document.createElement("div");
    option.className = "suggestion-item";
    option.id = `suggestion-${i}`;
    option.setAttribute("role", "option");
    option.textContent = text; // textContent, never innerHTML: the text comes from an LLM
    // mousedown instead of click: it fires before the input loses focus.
    option.addEventListener("mousedown", (event) => {
      event.preventDefault();
      choose(i);
    });
    dropdown.append(option);
  });

  if (mode === "mock") {
    const note = document.createElement("div");
    note.className = "suggestions-note";
    note.textContent = "Mock mode: canned suggestions (no LLM configured)";
    dropdown.append(note);
  }
  show();
}

function onKeyDown(event) {
  if (event.key === "Escape") {
    // Also cancels suggestions that are still waiting for the debounce.
    clearTimeout(debounceTimer);
    hide();
    return;
  }
  if (dropdown.classList.contains("hidden") || items.length === 0) return;

  switch (event.key) {
    case "ArrowDown":
      event.preventDefault();
      highlight((active + 1) % items.length);
      break;
    case "ArrowUp":
      event.preventDefault();
      highlight((active - 1 + items.length) % items.length);
      break;
    case "Enter":
      if (active >= 0) {
        event.preventDefault(); // otherwise Enter would add a new line
        choose(active);
      }
      break;
  }
}

function highlight(index) {
  active = index;
  dropdown.querySelectorAll(".suggestion-item").forEach((el, i) => {
    el.classList.toggle("active", i === index);
    el.setAttribute("aria-selected", String(i === index));
  });
  input.setAttribute("aria-activedescendant", `suggestion-${index}`);
}

function choose(index) {
  replaceText(input, items[index]);
}

function showStatus(message, isError = false) {
  items = [];
  active = -1;
  const status = document.createElement("div");
  status.className = isError ? "suggestions-status error" : "suggestions-status";
  status.textContent = message;
  dropdown.replaceChildren(status);
  show();
}

function show() {
  dropdown.classList.remove("hidden");
  input.setAttribute("aria-expanded", "true");
}

function hide() {
  controller?.abort();
  dropdown.classList.add("hidden");
  input.setAttribute("aria-expanded", "false");
  input.removeAttribute("aria-activedescendant");
  items = [];
  active = -1;
}
