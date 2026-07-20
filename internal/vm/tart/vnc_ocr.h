#ifndef VNC_OCR_H
#define VNC_OCR_H

#include <stdint.h>

typedef struct {
	int found;
	int x;
	int y;
	int width;
	int height;
} OCRTextResult;

// FindTextInRGBA searches for text in an RGBA image buffer using Apple Vision
// framework OCR. Returns the bounding box of the first case-insensitive
// substring match. Coordinates use top-left origin in pixel space.
OCRTextResult FindTextInRGBA(const uint8_t* rgba, int imgWidth, int imgHeight,
                             const char* searchText);

#endif
