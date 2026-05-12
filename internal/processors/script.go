package processors

// Script processor dispatch: stdin in, stdout out, extension → interpreter
// map (.py → python3, .sh → bash, .exe/.bin → direct exec). Non-zero exit
// triggers fallback to original input.
