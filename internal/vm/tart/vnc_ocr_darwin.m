#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#include "vnc_ocr.h"

OCRTextResult FindTextInRGBA(const uint8_t* rgba, int imgWidth, int imgHeight,
                             const char* searchText) {
	OCRTextResult result = {0, 0, 0, 0, 0};

	CGColorSpaceRef colorSpace = CGColorSpaceCreateDeviceRGB();
	if (!colorSpace) return result;

	CGContextRef ctx = CGBitmapContextCreate(
		(void*)rgba, imgWidth, imgHeight, 8, imgWidth * 4,
		colorSpace, kCGImageAlphaNoneSkipLast | kCGBitmapByteOrder32Big);
	CGColorSpaceRelease(colorSpace);
	if (!ctx) return result;

	CGImageRef cgImage = CGBitmapContextCreateImage(ctx);
	CGContextRelease(ctx);
	if (!cgImage) return result;

	NSString *search = [NSString stringWithUTF8String:searchText];

	__block OCRTextResult blockResult = {0, 0, 0, 0, 0};

	VNRecognizeTextRequest *request = [[VNRecognizeTextRequest alloc]
		initWithCompletionHandler:^(VNRequest *req, NSError *error) {
			if (error) return;

			for (VNRecognizedTextObservation *obs in req.results) {
				VNRecognizedText *candidate =
					[[obs topCandidates:1] firstObject];
				if (!candidate) continue;

				if ([candidate.string
						localizedCaseInsensitiveContainsString:search]) {
					CGRect bbox = obs.boundingBox;
					blockResult.found = 1;
					blockResult.x = (int)(bbox.origin.x * imgWidth);
					blockResult.y = (int)((1.0 - bbox.origin.y -
					                       bbox.size.height) * imgHeight);
					blockResult.width = (int)(bbox.size.width * imgWidth);
					blockResult.height = (int)(bbox.size.height * imgHeight);
					break;
				}
			}
		}];
	request.recognitionLevel = VNRequestTextRecognitionLevelAccurate;

	@autoreleasepool {
		VNImageRequestHandler *handler = [[VNImageRequestHandler alloc]
			initWithCGImage:cgImage
			        options:@{}];
		NSError *error = nil;
		[handler performRequests:@[request] error:&error];
	}

	CGImageRelease(cgImage);

	result = blockResult;
	return result;
}
