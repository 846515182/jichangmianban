/**
 * Echarts 按需注册模块（全局共享）
 */
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart, BarChart, PieChart, GaugeChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  TitleComponent,
  DataZoomComponent,
  GraphicComponent,
} from 'echarts/components'

use([
  CanvasRenderer,
  LineChart,
  BarChart,
  PieChart,
  GaugeChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  TitleComponent,
  DataZoomComponent,
  GraphicComponent,
])

// 暗色主题基础配置
export const darkThemeOption = {
  textStyle: {
    color: '#8b98a9',
  },
}

export const chartColors = ['#00f5d4', '#9d4edd', '#ff006e', '#ffbe0b', '#00d4ff']
