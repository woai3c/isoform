//go:build darwin

package ocr

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Vision -framework Foundation -framework CoreGraphics -framework ImageIO

#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#include <malloc/malloc.h>
#include <stdlib.h>
#include <string.h>

// ocr_result holds the C-side recognition output.
typedef struct {
    char *text;       // strdup'd UTF-8 text, caller must free
    double confidence;
    char *error;      // strdup'd error message or NULL
} ocr_result;

// ocr_observation holds a single text observation with bounding box.
typedef struct {
    char *text;
    double confidence;
    double x, y, width, height; // normalized [0,1], origin bottom-left (Vision convention)
} ocr_observation;

// ocr_detailed_result holds observations with bounding boxes.
typedef struct {
    ocr_observation *observations;
    int count;
    char *error;
} ocr_detailed_result;

static char *ocr_error_from_exception(NSException *exception) {
    NSString *name = [exception name];
    if (name == nil) {
        name = @"NSException";
    }
    NSString *reason = [exception reason];
    if (reason == nil) {
        reason = @"";
    }

    NSString *message = nil;
    if ([reason length] > 0) {
        message = [NSString stringWithFormat:@"native exception %@: %@", name, reason];
    } else {
        message = [NSString stringWithFormat:@"native exception %@", name];
    }
    return strdup([message UTF8String]);
}

static void ocr_free_observations(ocr_detailed_result *res) {
    if (!res || !res->observations) {
        return;
    }
    for (int i = 0; i < res->count; i++) {
        if (res->observations[i].text) {
            free(res->observations[i].text);
        }
    }
    free(res->observations);
    res->observations = NULL;
    res->count = 0;
}

static void ocr_release_request_objects(CGImageRef image, id request, id handler) {
    if (handler) {
        [handler release];
    }
    if (request) {
        [request release];
    }
    if (image) {
        CGImageRelease(image);
    }
}

static void ocr_relieve_malloc_pressure(void) {
    malloc_zone_pressure_relief(NULL, 0);
}

// Returns true when the Vision text-recognition API is available at runtime.
static bool ocr_is_supported(void) {
    if (@available(macOS 10.15, *)) {
        return true;
    }
    return false;
}

static CGImageRef ocr_load_image(const void *jpegData, int jpegLen, char **outError) {
    NSData *data = [NSData dataWithBytesNoCopy:(void *)jpegData
                                        length:(NSUInteger)jpegLen
                                  freeWhenDone:NO];
    CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL);
    if (!source) {
        *outError = strdup("failed to create image source from JPEG data");
        return NULL;
    }
    CGImageRef image = CGImageSourceCreateImageAtIndex(source, 0, NULL);
    CFRelease(source);
    if (!image) {
        *outError = strdup("failed to create CGImage from JPEG data");
        return NULL;
    }
    return image;
}

static ocr_result ocr_recognize(const void *jpegData, int jpegLen,
                                 const char **langs, int langCount,
                                 bool fastRecognition) {
    ocr_result res;
    memset(&res, 0, sizeof(res));

    if (@available(macOS 10.15, *)) {
        CGImageRef image = NULL;
        VNRecognizeTextRequest *request = nil;
        VNImageRequestHandler *handler = nil;

        @autoreleasepool {
            @try {
                image = ocr_load_image(jpegData, jpegLen, &res.error);
                if (!image) goto cleanup;

                request = [[VNRecognizeTextRequest alloc] init];
                request.recognitionLevel = fastRecognition
                    ? VNRequestTextRecognitionLevelFast
                    : VNRequestTextRecognitionLevelAccurate;
                request.usesLanguageCorrection = fastRecognition ? NO : YES;

                if (langCount > 0) {
                    NSMutableArray<NSString *> *langArray = [NSMutableArray arrayWithCapacity:langCount];
                    for (int i = 0; i < langCount; i++) {
                        [langArray addObject:[NSString stringWithUTF8String:langs[i]]];
                    }
                    request.recognitionLanguages = langArray;
                }

                handler = [[VNImageRequestHandler alloc]
                    initWithCGImage:image options:@{}];

                NSError *performError = nil;
                [handler performRequests:@[request] error:&performError];

                if (performError) {
                    res.error = strdup([[performError localizedDescription] UTF8String]);
                    goto cleanup;
                }

                NSMutableString *allText = [NSMutableString string];
                double totalConf = 0.0;
                NSUInteger count = 0;

                for (VNRecognizedTextObservation *obs in request.results) {
                    VNRecognizedText *top = [[obs topCandidates:1] firstObject];
                    if (top) {
                        if (allText.length > 0) {
                            [allText appendString:@"\n"];
                        }
                        [allText appendString:top.string];
                        totalConf += obs.confidence;
                        count++;
                    }
                }

                res.text = strdup(allText.length > 0 ? [allText UTF8String] : "");
                res.confidence = count > 0 ? totalConf / (double)count : 0.0;
            } @catch (NSException *exception) {
                if (res.error) {
                    free(res.error);
                }
                res.error = ocr_error_from_exception(exception);
            }

    cleanup:
            ocr_release_request_objects(image, request, handler);
        }

        ocr_relieve_malloc_pressure();
        return res;
    }

    res.error = strdup("OCR requires macOS 10.15 or later");
    return res;
}

// ocr_supported_langs queries the recognition languages available on this
// system. Returns a NULL-terminated array of strdup'd language strings, or
// NULL if the query API is unavailable (macOS < 12).
static char **ocr_supported_langs(int *outCount) {
    *outCount = 0;
    if (@available(macOS 12.0, *)) {
        @autoreleasepool {
            VNRecognizeTextRequest *request = [[VNRecognizeTextRequest alloc] init];
            request.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
            NSError *error = nil;
            NSArray<NSString *> *langs = [request supportedRecognitionLanguagesAndReturnError:&error];
            [request release];
            if (error || !langs || langs.count == 0) {
                return NULL;
            }
            int count = (int)langs.count;
            char **result = (char **)calloc(count, sizeof(char *));
            if (!result) return NULL;
            for (int i = 0; i < count; i++) {
                result[i] = strdup([langs[i] UTF8String]);
            }
            *outCount = count;
            return result;
        }
    }
    return NULL;
}

static ocr_detailed_result ocr_recognize_with_boxes(const void *jpegData, int jpegLen,
                                                      const char **langs, int langCount) {
    ocr_detailed_result res;
    memset(&res, 0, sizeof(res));

    if (@available(macOS 10.15, *)) {
        CGImageRef image = NULL;
        VNRecognizeTextRequest *request = nil;
        VNImageRequestHandler *handler = nil;

        @autoreleasepool {
            @try {
                image = ocr_load_image(jpegData, jpegLen, &res.error);
                if (!image) goto cleanup;

                request = [[VNRecognizeTextRequest alloc] init];
                request.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
                request.usesLanguageCorrection = YES;

                if (langCount > 0) {
                    NSMutableArray<NSString *> *langArray = [NSMutableArray arrayWithCapacity:langCount];
                    for (int i = 0; i < langCount; i++) {
                        [langArray addObject:[NSString stringWithUTF8String:langs[i]]];
                    }
                    request.recognitionLanguages = langArray;
                }

                handler = [[VNImageRequestHandler alloc]
                    initWithCGImage:image options:@{}];

                NSError *performError = nil;
                [handler performRequests:@[request] error:&performError];

                if (performError) {
                    res.error = strdup([[performError localizedDescription] UTF8String]);
                    goto cleanup;
                }

                NSArray *results = request.results;
                NSUInteger count = results.count;
                if (count == 0) {
                    goto cleanup;
                }

                res.observations = (ocr_observation *)calloc(count, sizeof(ocr_observation));
                if (!res.observations) {
                    res.error = strdup("failed to allocate memory for OCR observations");
                    goto cleanup;
                }
                res.count = (int)count;

                for (NSUInteger i = 0; i < count; i++) {
                    VNRecognizedTextObservation *obs = results[i];
                    VNRecognizedText *top = [[obs topCandidates:1] firstObject];
                    if (top) {
                        res.observations[i].text = strdup([top.string UTF8String]);
                    } else {
                        res.observations[i].text = strdup("");
                    }
                    res.observations[i].confidence = obs.confidence;
                    CGRect box = obs.boundingBox;
                    res.observations[i].x = box.origin.x;
                    res.observations[i].y = box.origin.y;
                    res.observations[i].width = box.size.width;
                    res.observations[i].height = box.size.height;
                }
            } @catch (NSException *exception) {
                ocr_free_observations(&res);
                if (res.error) {
                    free(res.error);
                }
                res.error = ocr_error_from_exception(exception);
            }

    cleanup:
            ocr_release_request_objects(image, request, handler);
        }

        ocr_relieve_malloc_pressure();
        return res;
    }

    res.error = strdup("OCR requires macOS 10.15 or later");
    return res;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// darwinEngine implements Engine using macOS Vision.framework.
type darwinEngine struct{}

// New creates a new platform-specific OCR Engine.
func New() Engine {
	return &darwinEngine{}
}

func (e *darwinEngine) IsSupported() bool {
	return bool(C.ocr_is_supported())
}

func (e *darwinEngine) SupportedLanguages() []string {
	var count C.int
	cLangs := C.ocr_supported_langs(&count)
	if cLangs == nil || count == 0 {
		return nil
	}
	n := int(count)
	slice := unsafe.Slice(cLangs, n)
	langs := make([]string, n)
	for i := 0; i < n; i++ {
		langs[i] = C.GoString(slice[i])
		C.free(unsafe.Pointer(slice[i]))
	}
	C.free(unsafe.Pointer(cLangs))
	return langs
}

// prepareCLangs converts Go string slice to C string array. Caller must free.
func prepareCLangs(langs []string) (cLangs **C.char, count int, cleanup func()) {
	count = len(langs)
	if count == 0 {
		return nil, 0, func() {}
	}
	cLangSlice := make([]*C.char, count)
	for i, l := range langs {
		cLangSlice[i] = C.CString(l)
	}
	return &cLangSlice[0], count, func() {
		for _, cl := range cLangSlice {
			C.free(unsafe.Pointer(cl))
		}
	}
}

func (e *darwinEngine) RecognizeJPEG(jpegData []byte, langs []string, prio Priority) (*Result, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}

	release := globalGate.acquire(prio)
	defer release()

	if result, handled, err := recognizeJPEGViaDarwinHelper(jpegData, langs, prio); handled {
		return result, err
	}

	cLangs, langCount, cleanup := prepareCLangs(langs)
	defer cleanup()

	res := C.ocr_recognize(
		unsafe.Pointer(&jpegData[0]),
		C.int(len(jpegData)),
		cLangs,
		C.int(langCount),
		C.bool(prio == PriorityLow),
	)

	if res.error != nil {
		errMsg := C.GoString(res.error)
		C.free(unsafe.Pointer(res.error))
		if res.text != nil {
			C.free(unsafe.Pointer(res.text))
		}
		return nil, fmt.Errorf("ocr: %s", errMsg)
	}

	text := ""
	if res.text != nil {
		text = C.GoString(res.text)
		C.free(unsafe.Pointer(res.text))
	}

	return &Result{
		Text:       text,
		Confidence: float64(res.confidence),
	}, nil
}

func (e *darwinEngine) RecognizeJPEGWithBoxes(jpegData []byte, langs []string, prio Priority) ([]TextObservation, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}

	release := globalGate.acquire(prio)
	defer release()

	if observations, handled, err := recognizeJPEGWithBoxesViaDarwinHelper(jpegData, langs, prio); handled {
		return observations, err
	}

	cLangs, langCount, cleanup := prepareCLangs(langs)
	defer cleanup()

	res := C.ocr_recognize_with_boxes(
		unsafe.Pointer(&jpegData[0]),
		C.int(len(jpegData)),
		cLangs,
		C.int(langCount),
	)

	if res.error != nil {
		errMsg := C.GoString(res.error)
		C.free(unsafe.Pointer(res.error))
		return nil, fmt.Errorf("ocr: %s", errMsg)
	}

	count := int(res.count)
	if count == 0 {
		return nil, nil
	}

	defer C.free(unsafe.Pointer(res.observations))

	// Convert C observations to Go slice.
	// Vision coordinates have origin at bottom-left; convert Y to top-left origin.
	cObs := unsafe.Slice(res.observations, count)
	observations := make([]TextObservation, count)
	for i := 0; i < count; i++ {
		text := ""
		if cObs[i].text != nil {
			text = C.GoString(cObs[i].text)
			C.free(unsafe.Pointer(cObs[i].text))
		}
		visionY := float64(cObs[i].y)
		h := float64(cObs[i].height)
		observations[i] = TextObservation{
			Text:       text,
			Confidence: float64(cObs[i].confidence),
			X:          float64(cObs[i].x),
			Y:          1.0 - visionY - h, // convert bottom-left to top-left origin
			Width:      float64(cObs[i].width),
			Height:     h,
		}
	}

	return observations, nil
}
