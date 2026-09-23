package string

// defaultInitcapDelimiters is the INITCAP default set from
// string_functions.md: <whitespace> [ ] ( ) { } / | \ < > ! ? @ " ^ # $
// & ~ _ , . : ; * % + -, where whitespace includes tab and newline.
var defaultInitcapDelimiters = []rune{
	' ', '\t', '\n', '\r', '\v', '\f', '[', ']', '(', ')', '{', '}', '/', '|', '\\',
	'<', '>', '!', '?', '@', '"', '^', '#', '$', '&',
	'~', '_', ',', '.', ':', ';', '*', '%', '+', '-',
}
