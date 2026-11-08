#ifndef HZ_VU_METER_H
#define HZ_VU_METER_H

#include <FL/Fl_Widget.H>

class HzVuMeter : public Fl_Widget {
public:
    HzVuMeter(int x, int y, int w, int h, const char *label = 0);
    void draw() override;
};

#endif
