#include <math.h>
#include <FL/Fl_Widget.H>

#include "vu_meter.h"

#define VU_OFFSET 6

float vu_left_db = -1000.0f;
float vu_right_db = -1000.0f;

static Fl_Widget *vu_display = NULL;

void vu_set_widget(Fl_Widget *widget)
{
    vu_display = widget;
}

void vu_init(void)
{
    vu_left_db = -1000.0f;
    vu_right_db = -1000.0f;
    if(vu_display != NULL)
        vu_display->redraw();
}

void vu_meter(short left, short right)
{
    if(left > 0)
        vu_left_db = -(20 * log10(32768 / (float)left)) + VU_OFFSET;
    else
        vu_left_db = -1000.0f;

    if(right > 0)
        vu_right_db = -(20 * log10(32768 / (float)right)) + VU_OFFSET;
    else
        vu_right_db = -1000.0f;

    if(vu_display != NULL)
        vu_display->redraw();
}
