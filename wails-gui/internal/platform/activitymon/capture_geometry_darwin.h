#include <math.h>
#include <limits.h>
#include <stdbool.h>

static bool capture_pixel_dimensions(double width, double height, double scale, int *outW, int *outH) {
    if (!isfinite(scale) || scale < 1) return false;
    double w = width * scale, h = height * scale;
    if (!isfinite(w) || !isfinite(h) || w < 1 || h < 1 || w > INT_MAX || h > INT_MAX) return false;
    *outW = (int)w;
    *outH = (int)h;
    return true;
}
