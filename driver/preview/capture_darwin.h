// C side of capture_darwin.go. See capture_darwin.m.
#include <stdint.h>

typedef struct {
	uint32_t id;
	char *app;
	char *title;
	int on_screen;
} pv_window;

int pv_available(void);
int pv_preflight(void);
void pv_request(void);
int pv_windows(pv_window **out, int *n);
void pv_free_windows(pv_window *w, int n);
uint32_t pv_focused(void);
int pv_capture(uint32_t id, int max_w, int max_h, uint8_t **rgba, int *w, int *h, char **err);
