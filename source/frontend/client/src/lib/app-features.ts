// Standard builds keep the LLM tools; portable builds can omit their entry points.
export const LLM_ENABLED = import.meta.env.VITE_LLM_ENABLED !== "false";
