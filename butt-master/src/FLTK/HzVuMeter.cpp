#include "HzVuMeter.h"
#include "vu_meter.h"
#include <FL/fl_draw.H>

static void draw_level_bar(int x, int y, int w, int h, float db, const char *label)
{
    fl_color(fl_rgb_color(8, 19, 31));
    fl_rectf(x, y, w, h);
    fl_color(fl_rgb_color(38, 58, 82));
    fl_rect(x, y, w, h);

    float norm = (db + 60.0f) / 66.0f;
    if(norm < 0.0f) norm = 0.0f;
    if(norm > 1.0f) norm = 1.0f;
    int fill = (int)(norm * (float)(w - 2));

    for(int i = 0; i < fill; i += 2)
    {
        float t = (float)i / (float)(w - 2);
        Fl_Color color;
        if(t < 0.65f)
            color = fl_rgb_color(52, 211, 153);
        else if(t < 0.85f)
            color = fl_rgb_color(251, 191, 36);
        else
            color = fl_rgb_color(251, 113, 133);
        fl_color(color);
        fl_rectf(x + 1 + i, y + 1, 2, h - 2);
    }

    if(fill > 1)
    {
        fl_color(fl_rgb_color(232, 241, 250));
        fl_rectf(x + fill, y + 1, 2, h - 2);
    }

    fl_color(fl_rgb_color(143, 166, 189));
    fl_font(FL_HELVETICA_BOLD, 10);
    fl_draw(label, x - 18, y + h - 2);
}

HzVuMeter::HzVuMeter(int x, int y, int w, int h, const char *label)
    : Fl_Widget(x, y, w, h, label)
{
    box(FL_NO_BOX);
}

void HzVuMeter::draw()
{
    fl_push_clip(x(), y(), w(), h());
    draw_level_bar(x() + 24, y() + 2, w() - 24, 14, vu_left_db, "L");
    draw_level_bar(x() + 24, y() + 26, w() - 24, 14, vu_right_db, "R");
    fl_pop_clip();
}
