//go:build linux && cgo

// Wrapper that requests client-side decorations as soon as Gio registers the
// xdg-decoration listener. See deco_linux.go for why the link flag in that
// file turns wl_proxy_add_listener into a call to __wrap_wl_proxy_add_listener.
//
// The libwayland symbols below are declared by hand rather than included from
// wayland-client.h: the package then needs no development headers of its own,
// and the declarations stay in sync with the ABI, not with a header that may
// not be installed. Types follow libwayland exactly, with struct wl_proxy kept
// opaque.

#include <stddef.h>
#include <stdint.h>
#include <string.h>

struct wl_proxy;

extern int __real_wl_proxy_add_listener(struct wl_proxy *proxy,
                                        void (**implementation)(void),
                                        void *data);
extern const char *wl_proxy_get_class(struct wl_proxy *proxy);
extern uint32_t wl_proxy_get_version(struct wl_proxy *proxy);
extern void *wl_proxy_marshal_flags(struct wl_proxy *proxy, uint32_t opcode,
                                    const void *interface, uint32_t version,
                                    uint32_t flags, ...);

// set_mode and its client_side value in xdg-decoration-unstable-v1.
enum { setModeRequest = 1, clientSide = 1 };

int __wrap_wl_proxy_add_listener(struct wl_proxy *proxy,
                                 void (**implementation)(void), void *data)
{
	int ret = __real_wl_proxy_add_listener(proxy, implementation, data);
	const char *class = wl_proxy_get_class(proxy);

	// Every listener registration goes through here, so the decoration object
	// is recognized by its interface name. The request goes out only once the
	// listener is in place, which is before the surface is first committed.
	if (ret == 0 && class != NULL && wl_proxy_get_version(proxy) >= 1 &&
	    strcmp(class, "zxdg_toplevel_decoration_v1") == 0) {
		wl_proxy_marshal_flags(proxy, setModeRequest, NULL,
		                       wl_proxy_get_version(proxy), 0,
		                       (uint32_t)clientSide);
	}
	return ret;
}
