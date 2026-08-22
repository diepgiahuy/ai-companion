package speech

// RequiresBatchAgent reports that Gemini delegation needs one final Companion
// response before native audio can start. ADK remains the product agent runtime.
func (*GeminiDelegatingBridge) RequiresBatchAgent() bool { return true }
