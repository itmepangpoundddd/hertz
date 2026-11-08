#ifndef HZ_UI_H
#define HZ_UI_H

#include <FL/Fl_Group.H>
#include <FL/Fl_Box.H>
#include <FL/Fl_Button.H>
#include <FL/Fl_Widget.H>

void hz_draw_round_rect(int x, int y, int w, int h, int radius,
                        Fl_Color fill, Fl_Color border);

class HzLabel : public Fl_Box {
public:
    HzLabel(int x, int y, int w, int h, const char *label = 0)
        : Fl_Box(x, y, w, h, label) {}
};

class HzCard : public Fl_Group {
public:
    HzCard(int x, int y, int w, int h, int radius = 18);
    void draw() override;

    Fl_Color fill;
    Fl_Color stroke;
    int radius;
};

class HzButton : public Fl_Button {
public:
    HzButton(int x, int y, int w, int h, const char *label = 0);
    void draw() override;
    int handle(int event) override;

    Fl_Color stroke;
    int radius;
    bool pressed;
};

class HzLogo : public Fl_Widget {
public:
    HzLogo(int x, int y, int size);
    void draw() override;
};

class HzBackdrop : public Fl_Widget {
public:
    HzBackdrop(int x, int y, int w, int h);
    void draw() override;
};

#endif
