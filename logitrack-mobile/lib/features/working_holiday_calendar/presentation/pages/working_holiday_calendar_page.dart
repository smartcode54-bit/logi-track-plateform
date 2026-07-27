import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/holiday_repository.dart';

class WorkingHolidayCalendarPage extends StatefulWidget {
  const WorkingHolidayCalendarPage({super.key});

  @override
  State<WorkingHolidayCalendarPage> createState() =>
      _WorkingHolidayCalendarPageState();
}

class _WorkingHolidayCalendarPageState extends State<WorkingHolidayCalendarPage>
    with SingleTickerProviderStateMixin {
  final _repo = HolidayRepository();
  int _selectedYear = DateTime.now().year;
  int _displayYear = DateTime.now().year;
  int _displayMonth = DateTime.now().month;
  late TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isTh = context.locale.languageCode == 'th';

    return Scaffold(
      appBar: AppBar(
        title: Text('working_holiday_calendar_title'.tr()),
        bottom: TabBar(
          controller: _tabController,
          tabs: [
            Tab(text: 'working_holiday_calendar_tab_month'.tr()),
            Tab(text: 'working_holiday_calendar_tab_list'.tr()),
          ],
        ),
      ),
      body: SafeArea(
        top: false,
        child: TabBarView(
          controller: _tabController,
          children: [
            _MonthView(
            repo: _repo,
            displayYear: _displayYear,
            displayMonth: _displayMonth,
            isTh: isTh,
            onMonthChanged: (year, month) {
              setState(() {
                _displayYear = year;
                _displayMonth = month;
                if (_selectedYear != year) _selectedYear = year;
              });
            },
          ),
          _ListView(
            repo: _repo,
            selectedYear: _selectedYear,
            isTh: isTh,
            onYearChanged: (year) {
              setState(() => _selectedYear = year);
            },
          ),
        ],
      ),
      ),
    );
  }
}

/// Month view: calendar grid for one month + list of holidays in that month.
class _MonthView extends StatelessWidget {
  const _MonthView({
    required this.repo,
    required this.displayYear,
    required this.displayMonth,
    required this.isTh,
    required this.onMonthChanged,
  });

  final HolidayRepository repo;
  final int displayYear;
  final int displayMonth;
  final bool isTh;
  final void Function(int year, int month) onMonthChanged;

  static const _weekdaysEn = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];
  static const _weekdaysTh = ['อา.', 'จ.', 'อ.', 'พ.', 'พฤ.', 'ศ.', 'ส.'];

  @override
  Widget build(BuildContext context) {
    final borderColor = Theme.of(context).colorScheme.outline.withOpacity(0.4);
    final sunColor = Colors.red.shade700;
    final satColor = Colors.blue.shade700;

    return StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
      stream: repo.watchHolidaysForYear(displayYear),
      builder: (context, snapshot) {
        if (snapshot.hasError) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'Error: ${snapshot.error}',
                textAlign: TextAlign.center,
              ),
            ),
          );
        }
        if (!snapshot.hasData) {
          return const Center(child: CircularProgressIndicator());
        }

        final docs = snapshot.data!.docs;
        final holidayDates = <int>{};
        final holidayList = <_HolidayItem>[];

        for (final doc in docs) {
          final d = doc.data();
          final date = (d['date'] as Timestamp?)?.toDate();
          if (date == null ||
              date.year != displayYear ||
              date.month != displayMonth) continue;
          holidayDates.add(date.day);
          final nameEn = d['holidayNameEN'] as String? ??
              d['name'] as String? ??
              '';
          final nameTh = d['holidayNameTH'] as String? ?? '';
          final name = isTh && nameTh.isNotEmpty
              ? nameTh
              : (nameEn.isNotEmpty ? nameEn : '');
          holidayList.add(_HolidayItem(day: date.day, name: name));
        }
        holidayList.sort((a, b) => a.day.compareTo(b.day));

        final monthStart = DateTime(displayYear, displayMonth, 1);
        final monthEnd = DateTime(displayYear, displayMonth + 1, 0);
        final leadingBlanksCount = monthStart.weekday % 7; // Sun=0, Mon=1, ...
        final daysInMonth = monthEnd.day;

        final calendarCells = <Widget>[];
        // Weekday header row with grid border and Sat/Sun styling
        for (int i = 0; i < 7; i++) {
          final isSun = i == 0;
          final isSat = i == 6;
          final textColor = isSun
              ? sunColor
              : isSat
                  ? satColor
                  : Theme.of(context)
                      .colorScheme
                      .onSurfaceVariant
                      .withOpacity(0.8);
          calendarCells.add(
            Container(
              decoration: BoxDecoration(
                border: Border(
                  right: BorderSide(color: borderColor),
                  bottom: BorderSide(color: borderColor),
                ),
                color: (isSun || isSat)
                    ? Theme.of(context)
                        .colorScheme
                        .surfaceContainerHighest
                        .withOpacity(0.4)
                    : null,
              ),
              child: Center(
                child: Text(
                  isTh ? _weekdaysTh[i] : _weekdaysEn[i],
                  style: Theme.of(context).textTheme.labelSmall?.copyWith(
                        fontWeight: FontWeight.w600,
                        color: textColor,
                      ),
                ),
              ),
            ),
          );
        }
        // Blank cells before first day
        for (int i = 0; i < leadingBlanksCount; i++) {
          calendarCells.add(
            Container(
              decoration: BoxDecoration(
                border: Border(
                  right: BorderSide(color: borderColor),
                  bottom: BorderSide(color: borderColor),
                ),
              ),
            ),
          );
        }
        for (int day = 1; day <= daysInMonth; day++) {
          final weekdayIndex = (leadingBlanksCount + (day - 1)) % 7;
          final isWeekend = weekdayIndex == 0 || weekdayIndex == 6;
          final isHoliday = holidayDates.contains(day);
          calendarCells.add(_DayCell(
            day: day,
            isHoliday: isHoliday,
            isWeekend: isWeekend,
            borderColor: borderColor,
          ));
        }

        final monthTitle = DateFormat.yMMM(context.locale.languageCode).format(monthStart);

        return RefreshIndicator(
          onRefresh: () async {},
          child: SingleChildScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 32),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    IconButton(
                      icon: const Icon(Icons.chevron_left),
                      onPressed: () {
                        if (displayMonth == 1) {
                          onMonthChanged(displayYear - 1, 12);
                        } else {
                          onMonthChanged(displayYear, displayMonth - 1);
                        }
                      },
                    ),
                    Text(
                      monthTitle,
                      style: Theme.of(context).textTheme.titleLarge?.copyWith(
                            fontWeight: FontWeight.w600,
                          ),
                    ),
                    IconButton(
                      icon: const Icon(Icons.chevron_right),
                      onPressed: () {
                        if (displayMonth == 12) {
                          onMonthChanged(displayYear + 1, 1);
                        } else {
                          onMonthChanged(displayYear, displayMonth + 1);
                        }
                      },
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                LayoutBuilder(
                  builder: (context, constraints) {
                    final width = constraints.maxWidth;
                    final cellWidth = width / 7;
                    final cellHeight = cellWidth / 1.1;
                    final rowCount = (calendarCells.length / 7).ceil();
                    final totalHeight = rowCount * cellHeight;
                    return SizedBox(
                      height: totalHeight,
                      child: Container(
                        decoration: BoxDecoration(
                          border: Border.all(color: borderColor),
                        ),
                        child: GridView.count(
                          shrinkWrap: true,
                          physics: const NeverScrollableScrollPhysics(),
                          crossAxisCount: 7,
                          mainAxisSpacing: 0,
                          crossAxisSpacing: 0,
                          childAspectRatio: 1.1,
                          children: calendarCells,
                        ),
                      ),
                    );
                  },
                ),
                const SizedBox(height: 20),
                Text(
                  'working_holiday_calendar_subtitle'.tr(),
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context)
                            .colorScheme
                            .onSurfaceVariant
                            .withOpacity(0.9),
                      ),
                ),
                const SizedBox(height: 8),
                if (holidayList.isEmpty)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 16),
                    child: Text(
                      'working_holiday_calendar_no_holidays_month'.tr(),
                      style: Theme.of(context).textTheme.bodyMedium,
                    ),
                  )
                else
                  ...holidayList.map(
                    (h) => Card(
                      margin: const EdgeInsets.only(bottom: 6),
                      child: ListTile(
                        title: Text(
                          h.name.isNotEmpty ? h.name : '—',
                          style: const TextStyle(fontWeight: FontWeight.w600),
                        ),
                        subtitle: Text(
                          DateFormat('EEEE, d MMM', context.locale.languageCode)
                              .format(DateTime(displayYear, displayMonth, h.day)),
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _HolidayItem {
  final int day;
  final String name;
  _HolidayItem({required this.day, required this.name});
}

class _DayCell extends StatelessWidget {
  const _DayCell({
    required this.day,
    required this.isHoliday,
    required this.isWeekend,
    required this.borderColor,
  });

  final int day;
  final bool isHoliday;
  final bool isWeekend;
  final Color borderColor;

  @override
  Widget build(BuildContext context) {
    const lightOrange = Color(0xFFFFE0B2); // Material orange 200
    const darkOrange = Color(0xFFE65100);  // Material orange 900
    final weekendBg = Theme.of(context)
        .colorScheme
        .surfaceContainerHighest
        .withOpacity(0.35);
    final weekendTextColor = Theme.of(context)
        .colorScheme
        .onSurfaceVariant
        .withOpacity(0.8);

    return Container(
      decoration: BoxDecoration(
        color: isHoliday
            ? lightOrange
            : isWeekend
                ? weekendBg
                : null,
        border: Border(
          right: BorderSide(color: borderColor),
          bottom: BorderSide(color: borderColor),
        ),
      ),
      child: Center(
        child: Text(
          '$day',
          style: TextStyle(
            fontWeight: isHoliday ? FontWeight.w700 : FontWeight.w500,
            color: isHoliday
                ? darkOrange
                : isWeekend
                    ? weekendTextColor
                    : Theme.of(context).colorScheme.onSurface,
          ),
        ),
      ),
    );
  }
}

/// List view: year dropdown + list of holidays for the year.
class _ListView extends StatelessWidget {
  const _ListView({
    required this.repo,
    required this.selectedYear,
    required this.isTh,
    required this.onYearChanged,
  });

  final HolidayRepository repo;
  final int selectedYear;
  final bool isTh;
  final void Function(int) onYearChanged;

  @override
  Widget build(BuildContext context) {
    return RefreshIndicator(
      onRefresh: () async {},
      child: StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
        stream: repo.watchHolidaysForYear(selectedYear),
        builder: (context, snapshot) {
          if (snapshot.hasError) {
            return CustomScrollView(
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: [
                _buildListHeader(context),
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        'Error: ${snapshot.error}',
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                ),
              ],
            );
          }
          if (!snapshot.hasData) {
            return CustomScrollView(
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: [
                _buildListHeader(context),
                const SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(child: CircularProgressIndicator()),
                ),
              ],
            );
          }
          final docs = snapshot.data!.docs;
          if (docs.isEmpty) {
            return CustomScrollView(
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: [
                _buildListHeader(context),
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          Icon(
                            Icons.calendar_today_outlined,
                            size: 64,
                            color: Theme.of(context)
                                .colorScheme
                                .primary
                                .withOpacity(0.5),
                          ),
                          const SizedBox(height: 16),
                          Text(
                            'working_holiday_calendar_no_holidays'.tr(),
                            textAlign: TextAlign.center,
                            style: Theme.of(context).textTheme.bodyLarge,
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ],
            );
          }
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              _buildListHeader(context),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final doc = docs[index];
                      final d = doc.data();
                      final date = (d['date'] as Timestamp?)?.toDate();
                      final type = d['type'] as String? ?? '';
                      final nameEn = d['holidayNameEN'] as String? ??
                          d['name'] as String? ??
                          '';
                      final nameTh = d['holidayNameTH'] as String? ?? '';
                      final name = isTh && nameTh.isNotEmpty
                          ? nameTh
                          : (nameEn.isNotEmpty ? nameEn : type);
                      final dateStr = date != null
                          ? DateFormat('dd MMM yyyy', context.locale.languageCode).format(date)
                          : '';

                      return Card(
                        margin: const EdgeInsets.only(bottom: 8),
                        child: ListTile(
                          title: Text(
                            name.isNotEmpty ? name : '—',
                            style: const TextStyle(fontWeight: FontWeight.w600),
                          ),
                          subtitle: dateStr.isNotEmpty ? Text(dateStr) : null,
                        ),
                      );
                    },
                    childCount: docs.length,
                  ),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Widget _buildListHeader(BuildContext context) {
    return SliverToBoxAdapter(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'working_holiday_calendar_subtitle'.tr(),
              style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(context)
                        .colorScheme
                        .onSurfaceVariant
                        .withOpacity(0.9),
                  ),
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                Text(
                  'working_holiday_calendar_year'.tr(),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(width: 12),
                DropdownButton<int>(
                  value: selectedYear,
                  items: List.generate(5, (i) {
                    final y = DateTime.now().year - 2 + i;
                    return DropdownMenuItem(value: y, child: Text('$y'));
                  }),
                  onChanged: (v) {
                    if (v != null) onYearChanged(v);
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
