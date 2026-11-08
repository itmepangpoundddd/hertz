#include "HzUi.h"
#include "hertz_theme.h"
#include <FL/fl_draw.H>

void hz_draw_round_rect(int x, int y, int w, int h, int radius,
                        Fl_Color fill, Fl_Color border)
{
    if(w <= 0 || h <= 0)
        return;
    if(radius < 0) radius = 0;
    if(radius > h / 2) radius = h / 2;
    if(radius > w / 2) radius = w / 2;

    if(border != 0)
    {
        hz_draw_round_rect(x, y, w, h, radius, border, 0);
        hz_draw_round_rect(x + 1, y + 1, w - 2, h - 2,
                           radius > 1 ? radius - 1 : 0, fill, 0);
        return;
    }

    fl_color(fill);
    fl_rectf(x + radius, y, w - 2 * radius, h);
    fl_rectf(x, y + radius, w, h - 2 * radius);
    if(radius > 0)
    {
        fl_pie(x, y, radius * 2, radius * 2, 0, 360);
        fl_pie(x + w - radius * 2, y, radius * 2, radius * 2, 0, 360);
        fl_pie(x, y + h - radius * 2, radius * 2, radius * 2, 0, 360);
        fl_pie(x + w - radius * 2, y + h - radius * 2,
               radius * 2, radius * 2, 0, 360);
    }
}

HzCard::HzCard(int x, int y, int w, int h, int r)
    : Fl_Group(x, y, w, h),
      fill(HZ_PANEL), stroke(HZ_BORDER), radius(r)
{
    box(FL_NO_BOX);
}

void HzCard::draw()
{
    hz_draw_round_rect(x(), y(), w(), h(), radius, stroke, 0);
    hz_draw_round_rect(x() + 1, y() + 1, w() - 2, h() - 2,
                       radius > 1 ? radius - 1 : 0, fill, 0);
    Fl_Group::draw();
}

HzButton::HzButton(int x, int y, int w, int h, const char *label)
    : Fl_Button(x, y, w, h, label),
      stroke(0), radius(-1), pressed(false)
{
    box(FL_NO_BOX);
    color(HZ_BUTTON);
    labelcolor(HZ_TEXT);
    labelfont(FL_HELVETICA_BOLD);
    labelsize(11);
}

void HzButton::draw()
{
    int r = radius >= 0 ? radius : h() / 2;
    Fl_Color fill = pressed ? fl_darker(color()) : color();
    if(stroke != 0)
    {
        hz_draw_round_rect(x(), y(), w(), h(), r, stroke, 0);
        hz_draw_round_rect(x() + 1, y() + 1, w() - 2, h() - 2,
                           r > 1 ? r - 1 : 0, fill, 0);
    }
    else
    {
        hz_draw_round_rect(x(), y(), w(), h(), r, fill, 0);
    }

    fl_font(labelfont(), labelsize());
    fl_color(labelcolor());
    fl_draw(label(), x(), y(), w(), h(), FL_ALIGN_CENTER);
}

int HzButton::handle(int event)
{
    if(event == FL_PUSH)
        pressed = true;
    else if(event == FL_RELEASE || event == FL_LEAVE)
        pressed = false;

    int result = Fl_Button::handle(event);
    if(event == FL_PUSH || event == FL_RELEASE)
        redraw();
    return result;
}

HzLogo::HzLogo(int x, int y, int size)
    : Fl_Widget(x, y, size, size)
{
    box(FL_NO_BOX);
}

void HzLogo::draw()
{
    fl_color(fl_rgb_color(219, 234, 254));
    fl_pie(x(), y(), w(), h(), 0, 360);

    static const int heights[7] = { 14, 24, 38, 48, 36, 26, 16 };
    const int bar_w = 4;
    const int gap = 3;
    const int total = 7 * bar_w + 6 * gap;
    int cx = x() + (w() - total) / 2;
    int base = y() + h() - 8;
    fl_color(HZ_ACCENT);
    for(int i = 0; i < 7; i++)
    {
        int bh = heights[i];
        fl_rectf(cx, base - bh, bar_w, bh);
        if(bh >= bar_w)
            fl_pie(cx, base - bh, bar_w, bar_w, 0, 360);
        cx += bar_w + gap;
    }
}

HzBackdrop::HzBackdrop(int x, int y, int w, int h)
    : Fl_Widget(x, y, w, h)
{
    box(FL_NO_BOX);
}

void HzBackdrop::draw()
{
    for(int yy = 0; yy < h(); yy++)
    {
        float t = (float)yy / (float)h();
        int r = (int)(234 - 12 * t);
        int g = (int)(243 - 8 * t);
        int b = 255;
        fl_color(fl_rgb_color(r, g, b));
        fl_line(x(), y() + yy, x() + w(), y() + yy);
    }

    fl_color(fl_rgb_color(214, 232, 255));
    fl_pie(x() - 100, y() + h() - 300, 420, 420, 0, 360);
    fl_color(fl_rgb_color(224, 238, 255));
    fl_pie(x() + w() - 280, y() + h() - 220, 360, 360, 0, 360);
}
