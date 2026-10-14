// shared.js: helpers used by suggestions.js, recommendations.js and
// variations.js. All requests to the JSON API go through postJSON, so
// network problems and API errors become user-friendly messages in
// exactly one way.

/**
 * Sends body as JSON to url with POST and returns the parsed answer.
 *
 * @param {string} url
 * @param {object} body
 * @param {{signal?: AbortSignal}} [options] signal cancels the request
 * @returns {Promise<{data: any, mode: string|null}>} mode is the
 *   X-LLM-Mode header ("live" or "mock") of the AI endpoints
 * @throws {Error} with a message that can be shown to the user, or an
 *   AbortError when the request was cancelled on purpose (see isAbort)
 */
export async function postJSON(url, body, { signal } = {}) {
  let response;
  try {
    response = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal,
    });
  } catch (err) {
    if (isAbort(err)) throw err;
    throw new Error("Cannot reach the server. Check that it is running.");
  }

  let data = null;
  try {
    data = await response.json();
  } catch (err) {
    if (isAbort(err)) throw err;
    // Not JSON: handled below.
  }

  if (!response.ok) {
    // Our API always explains errors as {"error": "..."}.
    throw new Error(data?.error || `Request failed (HTTP ${response.status}).`);
  }
  if (data === null) {
    throw new Error("The server sent an unexpected answer.");
  }
  return { data, mode: response.headers.get("X-LLM-Mode") };
}

/** Reports whether err comes from a request cancelled with AbortController. */
export function isAbort(err) {
  return err?.name === "AbortError";
}

/**
 * Replaces the text of the input from code (a picked suggestion or
 * variation). It fires an "input" event marked as programmatic, so other
 * features can react (the recommendation becomes outdated) while the
 * suggestions feature knows not to ask for new suggestions.
 *
 * @param {HTMLTextAreaElement} input
 * @param {string} value
 */
export function replaceText(input, value) {
  input.value = value;
  input.dispatchEvent(new CustomEvent("input", { detail: { programmatic: true } }));
  input.focus();
}
