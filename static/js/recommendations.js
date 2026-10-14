// recommendations.js: the "AI Recommend Banner" button (/api/recommend-banner).
//
// The server picks a banner with fixed rules (no LLM). The page shows the
// reason and the alternatives, highlights the recommended banner and
// offers a button to select it.
import { postJSON } from "./shared.js";

const input = document.getElementById("text-input");
const button = document.getElementById("ai-recommend-btn");
const result = document.getElementById("recommendation-result");
const buttonLabel = button.textContent;

button.addEventListener("click", recommend);
// A recommendation is about one text: once the text changes it is outdated.
input.addEventListener("input", clear);

async function recommend() {
  const text = input.value;
  if (text.trim() === "") {
    showError("Type some text first, then ask for a recommendation.");
    input.focus();
    return;
  }

  setLoading(true);
  try {
    const { data } = await postJSON("/api/recommend-banner", { text });
    showRecommendation(data);
  } catch (err) {
    showError(err.message);
  } finally {
    setLoading(false);
  }
}

function setLoading(loading) {
  button.disabled = loading;
  button.textContent = loading ? "Analyzing…" : buttonLabel;
  button.setAttribute("aria-busy", String(loading));
}

function showRecommendation({ recommended, reasoning, alternatives }) {
  const title = document.createElement("p");
  title.className = "rec-title";
  title.append("Recommended: ", strong(recommended));

  const reason = document.createElement("p");
  reason.className = "rec-reason";
  reason.textContent = reasoning;

  const list = document.createElement("ul");
  list.className = "rec-alternatives";
  for (const alt of alternatives) {
    const item = document.createElement("li");

    const bar = document.createElement("span");
    bar.className = "score-bar";
    bar.style.setProperty("--score", alt.score); // drawn by CSS as a bar
    bar.title = `Score ${alt.score}`;

    const name = strong(alt.banner);
    const details = document.createElement("span");
    details.textContent = ` ${alt.score.toFixed(2)}: ${alt.reason}`;

    item.append(bar, name, details);
    list.append(item);
  }

  const use = document.createElement("button");
  use.type = "button";
  use.className = "btn-link";
  use.textContent = `Use ${recommended}`;
  use.addEventListener("click", () => selectBanner(recommended));

  result.replaceChildren(title, reason, list, use);
  result.classList.remove("error", "hidden");
  highlightBanner(recommended);
}

function showError(message) {
  result.replaceChildren(message);
  result.classList.add("error");
  result.classList.remove("hidden");
  highlightBanner(null);
}

function clear() {
  result.classList.add("hidden");
  result.replaceChildren();
  highlightBanner(null);
}

/** Marks the banner option with the "recommended" style (null removes it). */
function highlightBanner(banner) {
  for (const option of document.querySelectorAll(".banner-option")) {
    option.classList.toggle("recommended", option.dataset.banner === banner);
  }
}

function selectBanner(banner) {
  const radio = document.querySelector(`input[name="banner"][value="${CSS.escape(banner)}"]`);
  if (radio) {
    radio.checked = true;
    radio.focus();
  }
}

function strong(text) {
  const el = document.createElement("strong");
  el.textContent = text;
  return el;
}
