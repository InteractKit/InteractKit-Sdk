package llm

// VARIABLE_CONFIRM_BRIDGE_PROMPT is used when the filler system has already
// spoken a phrase and we now need to deliver a variable confirmation question.
// The AI generates only the continuation — the part the listener hears after
// the filler trails off — so the two pieces form a single natural utterance.
// Input (user message): "Already spoken: \"<filler>\"\nMust deliver: \"<confirm>\""
// Output: {"text": "<continuation only>"}
const VARIABLE_CONFIRM_BRIDGE_PROMPT = `You are helping a voice AI agent deliver a spoken confirmation question.

The agent already started speaking a filler phrase (the caller can hear it). You must generate the spoken continuation that follows the filler naturally and delivers the confirmation question.

The caller will hear: [filler]...[your output] — it must sound like one uninterrupted sentence.

Rules:
- Do NOT repeat any part of the already-spoken filler phrase
- Begin naturally from where the filler trailed off (it ended with "...")
- Keep it brief and conversational — this is a phone call
- The output must include the read-back value and end with the verification question
- Output JSON: {"text": "continuation text here"}`

const FILLER_PROMPT = `Generate natural sentence openers (2-7 words ending with "...") that acknowledge partial user input and lead into a response. Vary phrasing widely. Respond in JSON: {"filler": "string", "skip": boolean, "prediction": "string"}.

**Core task:** User is mid-sentence. Your opener starts your reply without completing their thought or providing actual answers. Include a prediction of what the user is likely trying to say based on their partial input.

**Rules:**
1. For meaningful partial input → acknowledge their topic briefly, then pivot to offering help
   - Pattern: [light topic nod] + [offer assistance]
   - Keep acknowledgment generic—don't paraphrase or guess their exact meaning

2. For vague/unclear input → use simple, helpful openers that encourage them to continue

3. Always end with "..." to trail into the main response

4. Never answer questions or provide information—just set up that you're about to respond

5. Vary your openers constantly. Avoid repeating the same phrases across different turns

6. The "prediction" field should be a complete sentence guessing what the user is trying to say based on their partial input

**Examples (partial input → predicted full intent → appropriate filler):**

- **Partial:** "I'm trying to fix my kitchen sink..."
  **Prediction:** "I'm trying to fix my kitchen sink, but the pipe is leaking and I don't know what to do."
  **Filler:** "Okay, let's figure out that plumbing issue..."  
  *(Acknowledges the general topic and offers help without completing the sentence.)*

- **Partial:** "What's the weather like in Paris..."
  **Prediction:** "What's the weather like in Paris next week? I'm planning a trip."
  **Filler:** "Let me check the forecast for you..."  
  *(Generic nod to the query, then an offer to assist.)*

- **Partial:** "My computer is running really slow..."
  **Prediction:** "My computer is running really slow, especially when I open multiple tabs. How can I speed it up?"
  **Filler:** "I can help you with that slowdown..."  
  *(Acknowledges the problem area without diagnosing or solving.)*

- **Partial:** "I need a recipe for..."
  **Prediction:** "I need a recipe for chocolate chip cookies that are soft and chewy."
  **Filler:** "I'd be happy to help you find a good recipe..."  
  *(Acknowledges the request without completing it.)*

- **Partial:** "Can you explain how..."
  **Prediction:** "Can you explain how photosynthesis works in simple terms?"
  **Filler:** "I can help explain that concept..."  
  *(Offers assistance without diving into the answer.)*

- **Partial:** "Where is the nearest..."
  **Prediction:** "Where is the nearest coffee shop that's open right now?"
  **Filler:** "Let me help you find that location..."  
  *(Generic offer to assist with location-finding.)*

**Skip (skip: true) for:**
- False starts ("um", "uh", "so", "well")
- Mid-personal information ("My email is...", "My number is...", "I live at...")

**For skipped cases, prediction should be empty string:**
{"filler": "", "skip": true, "prediction": ""}`
