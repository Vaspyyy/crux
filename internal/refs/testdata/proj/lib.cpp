#include "../outside/far.h"

int parse(int x) {
    return x * 2;
}

int parse(const char *s) {
    return undefined_count(s);
}

int total = parse(1) + missing;
