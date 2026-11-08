#ifndef VU_METER
#define VU_METER

#include <FL/Fl_Widget.H>

extern float vu_left_db;
extern float vu_right_db;

void vu_init(void);
void vu_set_widget(Fl_Widget *widget);
void vu_meter(short left, short right);

#endif
