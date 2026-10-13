// Window list and window capture for capture_darwin.go. The window list
// comes from CGWindowListCopyWindowInfo, which is synchronous and cheap
// enough to read every focus poll. Capture uses ScreenCaptureKit's
// SCScreenshotManager (macOS 14), which captures a window even when other
// windows cover it.

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <stdlib.h>
#include <string.h>
#include "capture_darwin.h"

// A window smaller than this on either side is a helper (a tooltip, a
// status item's backing window), not something to preview.
static const double kMinSide = 40;

// Seconds to wait for a ScreenCaptureKit completion handler.
static const int64_t kTimeout = 5;

int pv_available(void) {
	// ScreenCaptureKit needs a window server connection; asking for the
	// main display opens one in a process with no NSApplication.
	(void)CGMainDisplayID();
	if (@available(macOS 14.0, *)) {
		return 1;
	}
	return 0;
}

int pv_preflight(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

void pv_request(void) { (void)CGRequestScreenCaptureAccess(); }

static char *copy_str(id v) {
	if (![v isKindOfClass:[NSString class]]) {
		return strdup("");
	}
	const char *s = [(NSString *)v UTF8String];
	return strdup(s ? s : "");
}

// usable reports whether d, one window list entry, is a normal window big
// enough to preview.
static BOOL usable(NSDictionary *d) {
	if ([d[(__bridge id)kCGWindowLayer] intValue] != 0) {
		return NO;
	}
	if ([d[(__bridge id)kCGWindowAlpha] doubleValue] <= 0) {
		return NO;
	}
	CGRect r;
	if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)d[(__bridge id)kCGWindowBounds], &r)) {
		return NO;
	}
	return r.size.width >= kMinSide && r.size.height >= kMinSide;
}

int pv_windows(pv_window **out, int *n) {
	@autoreleasepool {
		CFArrayRef list = CGWindowListCopyWindowInfo(
			kCGWindowListOptionAll | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
		if (list == NULL) {
			return -1;
		}
		NSArray *arr = CFBridgingRelease(list);
		pv_window *ws = calloc(arr.count + 1, sizeof(pv_window));
		int k = 0;
		for (NSDictionary *d in arr) {
			if (!usable(d)) {
				continue;
			}
			ws[k].id = [d[(__bridge id)kCGWindowNumber] unsignedIntValue];
			ws[k].app = copy_str(d[(__bridge id)kCGWindowOwnerName]);
			ws[k].title = copy_str(d[(__bridge id)kCGWindowName]);
			ws[k].on_screen = [d[(__bridge id)kCGWindowIsOnscreen] boolValue] ? 1 : 0;
			k++;
		}
		*out = ws;
		*n = k;
		return 0;
	}
}

void pv_free_windows(pv_window *ws, int n) {
	for (int i = 0; i < n; i++) {
		free(ws[i].app);
		free(ws[i].title);
	}
	free(ws);
}

// pv_focused returns the frontmost on-screen normal window. The on-screen
// list is ordered front to back, so this is the focused app's front
// window without needing the Accessibility permission.
uint32_t pv_focused(void) {
	@autoreleasepool {
		CFArrayRef list = CGWindowListCopyWindowInfo(
			kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
		if (list == NULL) {
			return 0;
		}
		NSArray *arr = CFBridgingRelease(list);
		for (NSDictionary *d in arr) {
			if (usable(d)) {
				return [d[(__bridge id)kCGWindowNumber] unsignedIntValue];
			}
		}
		return 0;
	}
}

static int fail(char **err, NSString *msg) {
	*err = strdup(msg.UTF8String);
	return -1;
}

int pv_capture(uint32_t wid, int max_w, int max_h, uint8_t **rgba, int *out_w, int *out_h, char **err) {
	if (@available(macOS 14.0, *)) {
		@autoreleasepool {
			__block SCShareableContent *content = nil;
			__block NSError *cerr = nil;
			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			[SCShareableContent getShareableContentExcludingDesktopWindows:YES
			                                           onScreenWindowsOnly:NO
			                                             completionHandler:^(SCShareableContent *c, NSError *e) {
				content = c;
				cerr = e;
				dispatch_semaphore_signal(sem);
			}];
			if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, kTimeout * NSEC_PER_SEC)) != 0) {
				return fail(err, @"timed out listing shareable windows");
			}
			if (content == nil) {
				return fail(err, cerr ? cerr.localizedDescription : @"no shareable content");
			}

			SCWindow *win = nil;
			for (SCWindow *w in content.windows) {
				if (w.windowID == wid) {
					win = w;
					break;
				}
			}
			if (win == nil) {
				return fail(err, @"window is not shareable");
			}

			CGSize size = win.frame.size;
			if (size.width < 1 || size.height < 1) {
				return fail(err, @"window has no size");
			}
			double scale = MIN(max_w / size.width, max_h / size.height);
			SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
			cfg.width = MAX(1, (size_t)lround(size.width * scale));
			cfg.height = MAX(1, (size_t)lround(size.height * scale));
			cfg.scalesToFit = YES;
			cfg.showsCursor = NO;
			cfg.ignoreShadowsSingleWindow = YES;
			SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:win];

			__block CGImageRef img = NULL;
			[SCScreenshotManager captureImageWithFilter:filter
			                              configuration:cfg
			                          completionHandler:^(CGImageRef image, NSError *e) {
				if (image != NULL) {
					img = CGImageRetain(image);
				}
				cerr = e;
				dispatch_semaphore_signal(sem);
			}];
			if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, kTimeout * NSEC_PER_SEC)) != 0) {
				// The handler may still run and retain an image; that leaks
				// one small image, which is better than racing it.
				return fail(err, @"timed out capturing window");
			}
			if (img == NULL) {
				return fail(err, cerr ? cerr.localizedDescription : @"capture returned no image");
			}

			size_t w = CGImageGetWidth(img), h = CGImageGetHeight(img);
			uint8_t *pix = calloc(w * h, 4);
			CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
			CGContextRef ctx = CGBitmapContextCreate(pix, w, h, 8, w * 4, cs,
			                                         kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
			CGColorSpaceRelease(cs);
			if (ctx == NULL) {
				free(pix);
				CGImageRelease(img);
				return fail(err, @"cannot make bitmap context");
			}
			CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), img);
			CGContextRelease(ctx);
			CGImageRelease(img);

			*rgba = pix;
			*out_w = (int)w;
			*out_h = (int)h;
			return 0;
		}
	}
	return fail(err, @"ScreenCaptureKit screenshots need macOS 14 or later");
}
