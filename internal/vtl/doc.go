// Package vtl is a small Apache Velocity (VTL) engine covering the subset AWS
// mapping templates use: #set, #if/#elseif/#else, #foreach (capped at 1000
// iterations), #break, #stop, #return, comments, references with property,
// index and method access, literals, ranges, maps and the usual operators,
// plus a Java-like method bridge for strings, lists and maps.
//
// Host values such as API Gateway's $input and $util plug in through Object.
// A null reference renders as an empty string. #macro, #define, #parse,
// #include and #evaluate are rejected at parse time. Every render runs under a
// step budget and the caller's context deadline.
package vtl
