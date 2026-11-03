#ifndef ISOFORM_MENUBAR_POPOVER_H
#define ISOFORM_MENUBAR_POPOVER_H
#include <stdint.h>
int isoformMenuBarAttach(void *item);
void isoformMenuBarDetach(void);
void isoformMenuBarInvalidate(void);
void isoformMenuBarUpdate(const char *json);
void isoformMenuBarPreload(const char *html, const char *json);
void isoformMenuBarError(const char *message);
void isoformMenuBarPlayback(const char *json);
#endif
