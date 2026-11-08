#include "HzVuMeter.h"
#include "vu_meter.h"
#include "hertz_theme.h"
#include <FL/fl_draw.H>
#include <math.h>

static float bar_pattern(int i)
{
    float p = 0.18f
            + 0.42f * (0.5f + 0.5f * sinf((float)i * 0.78f))
            + 0.28f * (0.5f + 0.5f * sinf((float)i * 0.31f + 1.7f));
    if(p > 1.0f) p = 1.0f;
    if(p < 0.12f) p = 0.12f;
    return p;
}

static float level_norm(float db)
{
    float n = (db + 60.0f) / 66.0f;
    if(n < 0.0f) n = 0.0f;
    if(n > 1.0f) n = 1.0f;
    return n;
}

static void draw_led_column(int x, int y, int w, int h, float norm, const char *label)
{
    fl_color(HZ_MUTED);
    fl_font(FL_HELVETICA_BOLD, 9);
    fl_draw(label, x + w / 2 - 3, y - 6);

    const int segments = 5;
    int seg_h = (h - (segments - 1) * 3) / segments;
    for(int i = 0; i < segments; i++)
    {
        float threshold = 0.12f + i * 0.17f;
        Fl_Color color;
        if(norm > threshold)
        {
            if(i < 3)
                color = HZ_GOOD;
            else if(i == 3)
                color = HZ_WARN;
            else
                color = HZ_BAD;
        }
        else
        {
            color = fl_rgb_color(226, 232, 240);
        }
        fl_color(color);
        fl_rectf(x, y + i * (seg_h + 3), w, seg_h);
    }
}

HzVuMeter::HzVuMeter(int x, int y, int w, int h, const char *label)
    : Fl_Widget(x, y, w, h, label)
{
    box(FL_NO_BOX);
}

void HzVuMeter::draw()
{
    fl_push_clip(x(), y(), w(), h());

    const int bar_count = 40;
    int led_area = 82;
    int area_w = w() - led_area;
    float step = (float)area_w / (float)bar_count;
    int base = y() + h() - 6;
    int max_h = h() - 18;
    float norm = level_norm(vu_left_db > vu_right_db ? vu_left_db : vu_right_db);
    float level = 0.28f + 0.72f * norm;

    for(int i = 0; i < bar_count; i++)
    {
        float p = bar_pattern(i) * level;
        int bh = 6 + (int)(p * (float)max_h);
        int bw = (int)(step * 0.58f);
        if(bw < 3) bw = 3;
        int bx = x() + (int)(i * step) + (int)((step - bw) / 2);
        int by = base - bh;

        Fl_Color color;
        if(i == 27 && norm > 0.45f)
            color = HZ_WARN;
        else
            color = fl_color_average(fl_rgb_color(147, 197, 253), HZ_ACCENT,
                                     (float)i / (float)(bar_count - 1));
        fl_color(color);
        fl_rectf(bx, by, bw, bh);
        if(bw >= 4 && bh >= 4)
            fl_pie(bx, by - 1, bw, bw, 0, 360);
    }

    int led_x = x() + w() - led_area + 16;
    int led_h = h() - 24;
    draw_led_column(led_x, y() + 12, 14, led_h, level_norm(vu_left_db), "L");
    draw_led_column(led_x + 32, y() + 12, 14, led_h, level_norm(vu_right_db), "R");

    fl_pop_clip();
}
